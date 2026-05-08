package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/agent/claude"
	"github.com/ainative/go-symphony/internal/agent/codex"
	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/ha"
	"github.com/ainative/go-symphony/internal/httpserver"
	"github.com/ainative/go-symphony/internal/orchestrator"
	"github.com/ainative/go-symphony/internal/tracker"
	"github.com/ainative/go-symphony/internal/tracker/linear"
	"github.com/ainative/go-symphony/internal/tracker/plane"
	"github.com/ainative/go-symphony/internal/workflow"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "WORKFLOW.md", "path to WORKFLOW.md config file")
	addr := flag.String("addr", "", "HTTP listen address (overrides config)")
	showVersion := flag.Bool("version", false, "print version and exit")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	// Positional arg overrides --config for convenience.
	if flag.NArg() > 0 {
		*cfgPath = flag.Arg(0)
	}

	if *showVersion {
		fmt.Printf("symphony %s\n", version)
		os.Exit(0)
	}

	setupLogger(*logLevel)
	slog.Info("starting symphony", "version", version, "config", *cfgPath)

	// Register adapters.
	tracker.RegisterTracker("linear", linear.NewAdapter)
	tracker.RegisterTracker("plane", plane.NewAdapter)
	agent.RegisterAgent("codex", codex.NewAdapter)
	agent.RegisterAgent("claude", claude.NewAdapter)

	// Load workflow config.
	frontMatter, promptBody, err := workflow.Load(*cfgPath)
	if err != nil {
		slog.Error("load workflow config", "error", err, "path", *cfgPath)
		os.Exit(1)
	}

	absCfgPath, _ := filepath.Abs(*cfgPath)
	cfg, err := config.Parse(frontMatter, filepath.Dir(absCfgPath))
	if err != nil {
		slog.Error("parse config", "error", err)
		os.Exit(1)
	}

	// Create tracker.
	trk, err := tracker.NewTracker(cfg.Tracker.Kind, trackerConfigMap(cfg))
	if err != nil {
		slog.Error("create tracker", "error", err, "kind", cfg.Tracker.Kind)
		os.Exit(1)
	}

	// Create agent.
	ag, err := agent.NewAgent(cfg.Agent.Kind, agentConfigFromSchema(cfg))
	if err != nil {
		slog.Error("create agent", "error", err, "kind", cfg.Agent.Kind)
		os.Exit(1)
	}

	// Create elector.
	var elector ha.Elector
	if cfg.HA.Enabled {
		raftElector, err := ha.NewRaftElector(ha.RaftConfig{
			Peers:         cfg.HA.RaftPeers,
			AdvertiseAddr: cfg.HA.AdvertiseAddr,
			RaftDir:       cfg.HA.RaftDir,
		})
		if err != nil {
			slog.Error("create raft elector", "error", err)
			os.Exit(1)
		}
		elector = raftElector
		slog.Info("HA enabled, using raft elector", "peers", cfg.HA.RaftPeers, "advertise", cfg.HA.AdvertiseAddr)
	} else {
		elector = ha.NewLocalElector()
	}

	// Start workflow store for hot-reload.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := workflow.NewStore(ctx, *cfgPath)
	if err != nil {
		slog.Error("create workflow store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Create orchestrator with closures that read from the store each tick.
	// This eliminates the data race from the previous goroutine-based approach.
	orch := orchestrator.New(
		trk,
		ag,
		elector,
		func() config.Schema {
			schema, _, err := store.Current()
			if err != nil || schema == nil {
				return *cfg
			}
			return *schema
		},
		func() string {
			_, body, err := store.Current()
			if err != nil {
				return promptBody
			}
			return body
		},
	)

	// Wire state replicator for HA mode.
	if sr, ok := elector.(ha.StateReplicator); ok {
		orch.SetReplicator(sr)
	}

	// Campaign for leadership.
	if err := elector.Campaign(ctx); err != nil {
		slog.Error("campaign for leadership", "error", err)
		os.Exit(1)
	}

	// Restore state from replicator after winning election.
	orch.RestoreFromReplicator()

	// Run orchestrator.
	go func() {
		if err := orch.Run(ctx); err != nil {
			slog.Error("orchestrator stopped", "error", err)
		}
	}()

	// Start HTTP server.
	serverCfg := cfg.Server
	if *addr != "" {
		host, port := splitHostPort(*addr)
		serverCfg.Host = host
		serverCfg.Port = port
	}
	maxConcurrent := cfg.Agent.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	srv := httpserver.New(orch.State, elector, orch, serverCfg, maxConcurrent)
	go func() {
		if err := srv.ListenAndServe(ctx); err != nil {
			slog.Error("http server stopped", "error", err)
		}
	}()

	slog.Info("symphony running", "addr", srv.Addr())

	// Wait for shutdown signal.
	<-ctx.Done()
	slog.Info("shutdown signal received, waiting for graceful shutdown...")

	elector.Resign()

	slog.Info("symphony shutdown complete")
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	logFormat := os.Getenv("SYMPHONY_LOG_FORMAT")

	var h slog.Handler
	opts := &slog.HandlerOptions{Level: l}

	switch logFormat {
	case "text":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

func trackerConfigMap(cfg *config.Schema) map[string]any {
	switch cfg.Tracker.Kind {
	case "linear":
		return map[string]any{
			"api_key":      cfg.Tracker.APIKey,
			"project_slug": cfg.Tracker.Linear.ProjectSlug,
			"endpoint":     cfg.Tracker.Linear.Endpoint,
		}
	case "plane":
		return map[string]any{
			"api_key":        cfg.Tracker.APIKey,
			"workspace_slug": cfg.Tracker.Plane.WorkspaceSlug,
			"project_id":     cfg.Tracker.Plane.ProjectID,
			"endpoint":       cfg.Tracker.Plane.Endpoint,
		}
	default:
		return map[string]any{"api_key": cfg.Tracker.APIKey}
	}
}

func agentConfigFromSchema(cfg *config.Schema) map[string]any {
	switch cfg.Agent.Kind {
	case "codex":
		m := map[string]any{
			"command":         cfg.Agent.Codex.Command,
			"approval_policy": cfg.Agent.Codex.ApprovalPolicy,
		}
		if cfg.Agent.Codex.TurnTimeoutMS > 0 {
			m["turn_timeout_ms"] = float64(cfg.Agent.Codex.TurnTimeoutMS)
		}
		if cfg.Agent.Codex.ReadTimeoutMS > 0 {
			m["read_timeout_ms"] = float64(cfg.Agent.Codex.ReadTimeoutMS)
		}
		if cfg.Agent.Codex.StallTimeoutMS > 0 {
			m["stall_timeout_ms"] = float64(cfg.Agent.Codex.StallTimeoutMS)
		}
		if cfg.Agent.Codex.ThreadSandbox != "" {
			m["thread_sandbox"] = cfg.Agent.Codex.ThreadSandbox
		}
		return m
	case "claude":
		m := map[string]any{
			"command": cfg.Agent.Claude.Command,
		}
		if cfg.Agent.Claude.PermissionMode != "" {
			m["permission_mode"] = cfg.Agent.Claude.PermissionMode
		}
		if len(cfg.Agent.Claude.AllowedTools) > 0 {
			m["allowed_tools"] = cfg.Agent.Claude.AllowedTools
		}
		if cfg.Agent.Claude.MaxTurns > 0 {
			m["max_turns"] = float64(cfg.Agent.Claude.MaxTurns)
		}
		return m
	default:
		return map[string]any{}
	}
}

// splitHostPort splits an address string into host and port.
// If no port is given, returns 0 (which means use the default).
func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}
