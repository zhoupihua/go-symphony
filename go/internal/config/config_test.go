package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_AllFieldsSpecified(t *testing.T) {
	raw := map[string]any{
		"tracker": map[string]any{
			"kind":    "linear",
			"api_key": "lin_test_123",
			"active_states":   []any{"active"},
			"terminal_states": []any{"done", "cancelled"},
			"linear": map[string]any{
				"project_slug": "my-project",
				"endpoint":     "https://custom.linear.app/graphql",
			},
			"plane": map[string]any{
				"workspace_slug": "my-workspace",
				"project_id":     "proj-123",
				"endpoint":       "https://custom.plane.so/api/",
			},
		},
		"polling": map[string]any{
			"interval_ms": 15000,
		},
		"workspace": map[string]any{
			"root": "/tmp/my_workspaces",
		},
		"hooks": map[string]any{
			"after_create":   "echo created",
			"before_run":     "echo before",
			"after_run":      "echo after",
			"before_remove":  "echo remove",
			"timeout_ms":     120000,
		},
		"agent": map[string]any{
			"kind":                 "codex",
			"max_concurrent":       5,
			"max_turns":            30,
			"max_retry_backoff_ms": 200000,
			"max_concurrent_by_state": map[string]any{
				"active": 3,
			},
			"codex": map[string]any{
				"command":             "codex app-server",
				"approval_policy":     "manual",
				"thread_sandbox":      "docker",
				"turn_sandbox_policy": "strict",
				"turn_timeout_ms":     600000,
				"read_timeout_ms":     60000,
				"stall_timeout_ms":    600000,
			},
			"claude": map[string]any{
				"command":         "claude-custom",
				"permission_mode": "dangerously-skip-permissions",
				"allowed_tools":   []any{"Bash", "Read"},
				"max_turns":       50,
			},
		},
		"worker": map[string]any{
			"ssh_hosts": []any{"worker1:22", "worker2:22"},
		},
		"ha": map[string]any{
			"enabled":        true,
			"raft_peers": []any{"10.0.0.5:9300", "10.0.0.6:9300"},
			"advertise_addr": "10.0.0.5:8080",
		},
		"server": map[string]any{
			"port": 9090,
			"host": "0.0.0.0",
		},
	}

	s, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// Tracker
	if s.Tracker.Kind != "linear" {
		t.Errorf("Tracker.Kind = %q, want %q", s.Tracker.Kind, "linear")
	}
	if s.Tracker.APIKey != "lin_test_123" {
		t.Errorf("Tracker.APIKey = %q, want %q", s.Tracker.APIKey, "lin_test_123")
	}
	if len(s.Tracker.ActiveStates) != 1 || s.Tracker.ActiveStates[0] != "active" {
		t.Errorf("Tracker.ActiveStates = %v, want [active]", s.Tracker.ActiveStates)
	}
	if len(s.Tracker.TerminalStates) != 2 {
		t.Errorf("Tracker.TerminalStates = %v, want 2 items", s.Tracker.TerminalStates)
	}
	if s.Tracker.Linear.ProjectSlug != "my-project" {
		t.Errorf("Tracker.Linear.ProjectSlug = %q, want %q", s.Tracker.Linear.ProjectSlug, "my-project")
	}
	if s.Tracker.Linear.Endpoint != "https://custom.linear.app/graphql" {
		t.Errorf("Tracker.Linear.Endpoint = %q, want custom endpoint", s.Tracker.Linear.Endpoint)
	}
	if s.Tracker.Plane.WorkspaceSlug != "my-workspace" {
		t.Errorf("Tracker.Plane.WorkspaceSlug = %q, want %q", s.Tracker.Plane.WorkspaceSlug, "my-workspace")
	}
	if s.Tracker.Plane.Endpoint != "https://custom.plane.so/api/" {
		t.Errorf("Tracker.Plane.Endpoint = %q, want custom endpoint", s.Tracker.Plane.Endpoint)
	}

	// Polling
	if s.Polling.IntervalMS != 15000 {
		t.Errorf("Polling.IntervalMS = %d, want 15000", s.Polling.IntervalMS)
	}

	// Workspace: absolute path from config preserved (platform-specific normalization)
	if !filepath.IsAbs(s.Workspace.Root) {
		t.Errorf("Workspace.Root = %q, want absolute path", s.Workspace.Root)
	}

	// Hooks
	if s.Hooks.AfterCreate != "echo created" {
		t.Errorf("Hooks.AfterCreate = %q, want %q", s.Hooks.AfterCreate, "echo created")
	}
	if s.Hooks.BeforeRun != "echo before" {
		t.Errorf("Hooks.BeforeRun = %q, want %q", s.Hooks.BeforeRun, "echo before")
	}
	if s.Hooks.AfterRun != "echo after" {
		t.Errorf("Hooks.AfterRun = %q, want %q", s.Hooks.AfterRun, "echo after")
	}
	if s.Hooks.BeforeRemove != "echo remove" {
		t.Errorf("Hooks.BeforeRemove = %q, want %q", s.Hooks.BeforeRemove, "echo remove")
	}
	if s.Hooks.TimeoutMS != 120000 {
		t.Errorf("Hooks.TimeoutMS = %d, want 120000", s.Hooks.TimeoutMS)
	}

	// Agent
	if s.Agent.Kind != "codex" {
		t.Errorf("Agent.Kind = %q, want %q", s.Agent.Kind, "codex")
	}
	if s.Agent.MaxConcurrent != 5 {
		t.Errorf("Agent.MaxConcurrent = %d, want 5", s.Agent.MaxConcurrent)
	}
	if s.Agent.MaxTurns != 30 {
		t.Errorf("Agent.MaxTurns = %d, want 30", s.Agent.MaxTurns)
	}
	if s.Agent.MaxRetryBackoffMS != 200000 {
		t.Errorf("Agent.MaxRetryBackoffMS = %d, want 200000", s.Agent.MaxRetryBackoffMS)
	}
	if s.Agent.MaxConcurrentByState["active"] != 3 {
		t.Errorf("Agent.MaxConcurrentByState[\"active\"] = %d, want 3", s.Agent.MaxConcurrentByState["active"])
	}

	// Codex
	if s.Agent.Codex.Command != "codex app-server" {
		t.Errorf("Agent.Codex.Command = %q, want %q", s.Agent.Codex.Command, "codex app-server")
	}
	if s.Agent.Codex.ApprovalPolicy != "manual" {
		t.Errorf("Agent.Codex.ApprovalPolicy = %q, want %q", s.Agent.Codex.ApprovalPolicy, "manual")
	}
	if s.Agent.Codex.ThreadSandbox != "docker" {
		t.Errorf("Agent.Codex.ThreadSandbox = %q, want %q", s.Agent.Codex.ThreadSandbox, "docker")
	}
	if s.Agent.Codex.TurnSandboxPolicy != "strict" {
		t.Errorf("Agent.Codex.TurnSandboxPolicy = %q, want %q", s.Agent.Codex.TurnSandboxPolicy, "strict")
	}
	if s.Agent.Codex.TurnTimeoutMS != 600000 {
		t.Errorf("Agent.Codex.TurnTimeoutMS = %d, want 600000", s.Agent.Codex.TurnTimeoutMS)
	}
	if s.Agent.Codex.ReadTimeoutMS != 60000 {
		t.Errorf("Agent.Codex.ReadTimeoutMS = %d, want 60000", s.Agent.Codex.ReadTimeoutMS)
	}
	if s.Agent.Codex.StallTimeoutMS != 600000 {
		t.Errorf("Agent.Codex.StallTimeoutMS = %d, want 600000", s.Agent.Codex.StallTimeoutMS)
	}

	// Claude
	if s.Agent.Claude.Command != "claude-custom" {
		t.Errorf("Agent.Claude.Command = %q, want %q", s.Agent.Claude.Command, "claude-custom")
	}
	if s.Agent.Claude.PermissionMode != "dangerously-skip-permissions" {
		t.Errorf("Agent.Claude.PermissionMode = %q, want %q", s.Agent.Claude.PermissionMode, "dangerously-skip-permissions")
	}
	if len(s.Agent.Claude.AllowedTools) != 2 {
		t.Errorf("Agent.Claude.AllowedTools = %v, want 2 items", s.Agent.Claude.AllowedTools)
	}
	if s.Agent.Claude.MaxTurns != 50 {
		t.Errorf("Agent.Claude.MaxTurns = %d, want 50", s.Agent.Claude.MaxTurns)
	}

	// Worker
	if len(s.Worker.SSHHosts) != 2 {
		t.Errorf("Worker.SSHHosts = %v, want 2 items", s.Worker.SSHHosts)
	}

	// HA
	if !s.HA.Enabled {
		t.Error("HA.Enabled = false, want true")
	}
	if len(s.HA.RaftPeers) != 2 {
		t.Errorf("HA.RaftPeers = %v, want 2 items", s.HA.RaftPeers)
	}
	if s.HA.AdvertiseAddr != "10.0.0.5:8080" {
		t.Errorf("HA.AdvertiseAddr = %q, want %q", s.HA.AdvertiseAddr, "10.0.0.5:8080")
	}

	// Server
	if s.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want 9090", s.Server.Port)
	}
	if s.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want %q", s.Server.Host, "0.0.0.0")
	}
}

func TestParse_EmptyConfig_AllDefaults(t *testing.T) {
	raw := map[string]any{}

	_, err := Parse(raw, "")
	if err == nil {
		t.Fatal("Parse() expected validation error for empty config, got nil")
	}
}

func TestParse_PartialConfig(t *testing.T) {
	raw := map[string]any{
		"tracker": map[string]any{
			"kind":    "plane",
			"api_key": "plane_key",
			"plane": map[string]any{
				"workspace_slug": "my-ws",
				"project_id":     "proj-1",
			},
		},
		"polling": map[string]any{
			"interval_ms": 5000,
		},
		"agent": map[string]any{
			"kind":          "claude",
			"max_concurrent": 3,
		},
	}

	s, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// Specified values preserved
	if s.Tracker.Kind != "plane" {
		t.Errorf("Tracker.Kind = %q, want %q", s.Tracker.Kind, "plane")
	}
	if s.Tracker.APIKey != "plane_key" {
		t.Errorf("Tracker.APIKey = %q, want %q", s.Tracker.APIKey, "plane_key")
	}
	if s.Polling.IntervalMS != 5000 {
		t.Errorf("Polling.IntervalMS = %d, want 5000", s.Polling.IntervalMS)
	}
	if s.Agent.Kind != "claude" {
		t.Errorf("Agent.Kind = %q, want %q", s.Agent.Kind, "claude")
	}
	if s.Agent.MaxConcurrent != 3 {
		t.Errorf("Agent.MaxConcurrent = %d, want 3", s.Agent.MaxConcurrent)
	}

	// Defaults for unspecified fields
	if s.Tracker.Linear.Endpoint != "https://api.linear.app/graphql" {
		t.Errorf("Tracker.Linear.Endpoint = %q, want default", s.Tracker.Linear.Endpoint)
	}
	wantActive := []string{"Todo", "In Progress"}
	if len(s.Tracker.ActiveStates) != len(wantActive) {
		t.Errorf("Tracker.ActiveStates = %v, want default %v", s.Tracker.ActiveStates, wantActive)
	}
	wantTerminal := []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	if len(s.Tracker.TerminalStates) != len(wantTerminal) {
		t.Errorf("Tracker.TerminalStates = %v, want default %v", s.Tracker.TerminalStates, wantTerminal)
	}
	if s.Agent.MaxTurns != 20 {
		t.Errorf("Agent.MaxTurns = %d, want 20 (default)", s.Agent.MaxTurns)
	}
	if s.Agent.MaxRetryBackoffMS != 300000 {
		t.Errorf("Agent.MaxRetryBackoffMS = %d, want 300000 (default)", s.Agent.MaxRetryBackoffMS)
	}
	if s.Agent.Codex.ApprovalPolicy != "auto" {
		t.Errorf("Agent.Codex.ApprovalPolicy = %q, want %q (default)", s.Agent.Codex.ApprovalPolicy, "auto")
	}
	if s.Agent.Codex.Command != "codex app-server" {
		t.Errorf("Agent.Codex.Command = %q, want %q (default)", s.Agent.Codex.Command, "codex app-server")
	}
	if s.Agent.Claude.Command != "claude" {
		t.Errorf("Agent.Claude.Command = %q, want %q (default)", s.Agent.Claude.Command, "claude")
	}
	if s.Hooks.TimeoutMS != 60000 {
		t.Errorf("Hooks.TimeoutMS = %d, want 60000 (default)", s.Hooks.TimeoutMS)
	}
	if s.Server.Host != "localhost" {
		t.Errorf("Server.Host = %q, want %q (default)", s.Server.Host, "localhost")
	}
}

func TestParse_WorkspaceRootTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	raw := map[string]any{
		"workspace": map[string]any{
			"root": "~/symphony_ws",
		},
		"tracker": map[string]any{
			"kind":    "linear",
			"api_key": "test_key",
			"linear": map[string]any{
				"project_slug": "my-proj",
			},
		},
		"agent": map[string]any{
			"kind": "codex",
		},
	}

	s, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := filepath.Join(home, "symphony_ws")
	if s.Workspace.Root != want {
		t.Errorf("Workspace.Root = %q, want %q", s.Workspace.Root, want)
	}
}

func TestParse_RelativeWorkspaceRoot(t *testing.T) {
	raw := map[string]any{
		"workspace": map[string]any{
			"root": "workspaces",
		},
		"tracker": map[string]any{
			"kind":    "linear",
			"api_key": "test_key",
			"linear": map[string]any{
				"project_slug": "my-proj",
			},
		},
		"agent": map[string]any{
			"kind": "codex",
		},
	}

	s, err := Parse(raw, "/project/myrepo")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	// Relative path resolved against workflowDir
	if !filepath.IsAbs(s.Workspace.Root) {
		t.Errorf("Workspace.Root = %q, want absolute path after resolution", s.Workspace.Root)
	}
	if !strings.HasSuffix(filepath.ToSlash(s.Workspace.Root), "project/myrepo/workspaces") {
		t.Errorf("Workspace.Root = %q, want suffix project/myrepo/workspaces", s.Workspace.Root)
	}
}

func TestParse_DefaultActiveAndTerminalStates(t *testing.T) {
	raw := map[string]any{
		"tracker": map[string]any{
			"kind":    "linear",
			"api_key": "test_key",
			"linear": map[string]any{
				"project_slug": "my-proj",
			},
		},
		"agent": map[string]any{
			"kind": "codex",
		},
	}

	s, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	wantActive := []string{"Todo", "In Progress"}
	if len(s.Tracker.ActiveStates) != len(wantActive) {
		t.Fatalf("Tracker.ActiveStates = %v, want %v", s.Tracker.ActiveStates, wantActive)
	}
	for i, v := range wantActive {
		if s.Tracker.ActiveStates[i] != v {
			t.Errorf("Tracker.ActiveStates[%d] = %q, want %q", i, s.Tracker.ActiveStates[i], v)
		}
	}

	wantTerminal := []string{"Closed", "Cancelled", "Canceled", "Duplicate", "Done"}
	if len(s.Tracker.TerminalStates) != len(wantTerminal) {
		t.Fatalf("Tracker.TerminalStates = %v, want %v", s.Tracker.TerminalStates, wantTerminal)
	}
	for i, v := range wantTerminal {
		if s.Tracker.TerminalStates[i] != v {
			t.Errorf("Tracker.TerminalStates[%d] = %q, want %q", i, s.Tracker.TerminalStates[i], v)
		}
	}
}

func TestParse_DefaultCodexTimeouts(t *testing.T) {
	raw := map[string]any{
		"tracker": map[string]any{
			"kind":    "linear",
			"api_key": "test_key",
			"linear": map[string]any{
				"project_slug": "my-proj",
			},
		},
		"agent": map[string]any{
			"kind": "codex",
		},
	}

	s, err := Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if s.Agent.Codex.TurnTimeoutMS != 3600000 {
		t.Errorf("Agent.Codex.TurnTimeoutMS = %d, want 3600000 (1h default)", s.Agent.Codex.TurnTimeoutMS)
	}
	if s.Agent.Codex.ReadTimeoutMS != 5000 {
		t.Errorf("Agent.Codex.ReadTimeoutMS = %d, want 5000 (5s default)", s.Agent.Codex.ReadTimeoutMS)
	}
}

func TestParse_AgentKindAutoDetect(t *testing.T) {
	tests := []struct {
		name      string
		raw       map[string]any
		wantKind  string
		wantError bool
	}{
		{
			name: "auto-detect codex from codex.command",
			raw: map[string]any{
				"tracker": map[string]any{
					"kind":    "linear",
					"api_key": "test_key",
					"linear": map[string]any{
						"project_slug": "my-proj",
					},
				},
				"agent": map[string]any{
					"codex": map[string]any{
						"command": "codex app-server",
					},
				},
			},
			wantKind: "codex",
		},
		{
			name: "explicit kind takes precedence over auto-detect",
			raw: map[string]any{
				"tracker": map[string]any{
					"kind":    "linear",
					"api_key": "test_key",
					"linear": map[string]any{
						"project_slug": "my-proj",
					},
				},
				"agent": map[string]any{
					"kind": "claude",
					"codex": map[string]any{
						"command": "codex app-server",
					},
				},
			},
			wantKind: "claude",
		},
		{
			name:      "no kind and no codex.command means empty kind",
			raw:       map[string]any{},
			wantKind:  "",
			wantError: true,
		},
		{
			name: "codex.command empty string gets default and auto-detects",
			raw: map[string]any{
				"tracker": map[string]any{
					"kind":    "linear",
					"api_key": "test_key",
					"linear": map[string]any{
						"project_slug": "my-proj",
					},
				},
				"agent": map[string]any{
					"codex": map[string]any{
						"command": "",
					},
				},
			},
			wantKind: "codex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.raw, "")
			if tt.wantError {
				if err == nil {
					t.Fatal("Parse() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if s.Agent.Kind != tt.wantKind {
				t.Errorf("Agent.Kind = %q, want %q", s.Agent.Kind, tt.wantKind)
			}
		})
	}
}

func TestParse_MissingRequiredFields_ValidationError(t *testing.T) {
	raw := map[string]any{
		"tracker": map[string]any{
			"api_key": "some_key",
		},
	}

	_, err := Parse(raw, "")
	if err == nil {
		t.Fatal("Parse() expected validation error for missing required fields, got nil")
	}
}

func TestResolveEnvVars(t *testing.T) {
	t.Run("resolves env var from string field", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "resolved_key_123")

		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "$TEST_API_KEY",
			},
		}

		err := resolveEnvVars(s)
		if err != nil {
			t.Fatalf("resolveEnvVars() error = %v", err)
		}
		if s.Tracker.APIKey != "resolved_key_123" {
			t.Errorf("Tracker.APIKey = %q, want %q", s.Tracker.APIKey, "resolved_key_123")
		}
	})

	t.Run("error on unset env var", func(t *testing.T) {
		os.Unsetenv("NONEXISTENT_VAR_XYZ")

		s := &Schema{
			Tracker: TrackerConfig{
				APIKey: "$NONEXISTENT_VAR_XYZ",
			},
		}

		err := resolveEnvVars(s)
		if err == nil {
			t.Fatal("resolveEnvVars() expected error for unset env var, got nil")
		}
		if !strings.Contains(err.Error(), "NONEXISTENT_VAR_XYZ") {
			t.Errorf("error %q should mention var name NONEXISTENT_VAR_XYZ", err.Error())
		}
		if !strings.Contains(err.Error(), "Tracker.APIKey") {
			t.Errorf("error %q should mention field path Tracker.APIKey", err.Error())
		}
	})

	t.Run("literal string without $ unchanged", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "literal_key",
			},
		}

		err := resolveEnvVars(s)
		if err != nil {
			t.Fatalf("resolveEnvVars() error = %v", err)
		}
		if s.Tracker.APIKey != "literal_key" {
			t.Errorf("Tracker.APIKey = %q, want %q (unchanged)", s.Tracker.APIKey, "literal_key")
		}
	})

	t.Run("resolves nested struct field", func(t *testing.T) {
		t.Setenv("TEST_SLUG", "my-resolved-slug")

		s := &Schema{
			Tracker: TrackerConfig{
				Kind: "linear",
				Linear: LinearConfig{
					ProjectSlug: "$TEST_SLUG",
				},
			},
		}

		err := resolveEnvVars(s)
		if err != nil {
			t.Fatalf("resolveEnvVars() error = %v", err)
		}
		if s.Tracker.Linear.ProjectSlug != "my-resolved-slug" {
			t.Errorf("Tracker.Linear.ProjectSlug = %q, want %q", s.Tracker.Linear.ProjectSlug, "my-resolved-slug")
		}
	})

	t.Run("resolves string in slice", func(t *testing.T) {
		t.Setenv("TEST_SSH_HOST", "worker.example.com:22")

		s := &Schema{
			Worker: WorkerConfig{
				SSHHosts: []string{"$TEST_SSH_HOST", "static-host:22"},
			},
		}

		err := resolveEnvVars(s)
		if err != nil {
			t.Fatalf("resolveEnvVars() error = %v", err)
		}
		if len(s.Worker.SSHHosts) != 2 {
			t.Fatalf("Worker.SSHHosts len = %d, want 2", len(s.Worker.SSHHosts))
		}
		if s.Worker.SSHHosts[0] != "worker.example.com:22" {
			t.Errorf("Worker.SSHHosts[0] = %q, want %q", s.Worker.SSHHosts[0], "worker.example.com:22")
		}
		if s.Worker.SSHHosts[1] != "static-host:22" {
			t.Errorf("Worker.SSHHosts[1] = %q, want %q", s.Worker.SSHHosts[1], "static-host:22")
		}
	})

	t.Run("error on empty env var value", func(t *testing.T) {
		t.Setenv("TEST_EMPTY_VAR", "")

		s := &Schema{
			Tracker: TrackerConfig{
				APIKey: "$TEST_EMPTY_VAR",
			},
		}

		err := resolveEnvVars(s)
		if err == nil {
			t.Fatal("resolveEnvVars() expected error for empty env var, got nil")
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid linear config", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "codex app-server",
				},
			},
		}

		err := Validate(s)
		if err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("valid plane config", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "plane",
				APIKey: "some_key",
				Plane: PlaneConfig{
					WorkspaceSlug: "my-workspace",
					ProjectID:     "proj-123",
				},
			},
			Agent: AgentConfig{
				Kind:   "claude",
				Claude: ClaudeConfig{Command: "claude"},
			},
		}

		err := Validate(s)
		if err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	t.Run("missing tracker.kind", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				APIKey: "some_key",
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "codex app-server",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "tracker.kind") {
			t.Errorf("error %q should mention tracker.kind", err.Error())
		}
	})

	t.Run("invalid tracker.kind", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "jira",
				APIKey: "some_key",
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "codex app-server",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "jira") {
			t.Errorf("error %q should mention invalid kind jira", err.Error())
		}
	})

	t.Run("missing agent.kind", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "agent.kind") {
			t.Errorf("error %q should mention agent.kind", err.Error())
		}
	})

	t.Run("invalid agent.kind", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
			Agent: AgentConfig{
				Kind: "copilot",
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "copilot") {
			t.Errorf("error %q should mention invalid kind copilot", err.Error())
		}
	})

	t.Run("missing api_key", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind: "linear",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "codex app-server",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "tracker.api_key") {
			t.Errorf("error %q should mention tracker.api_key", err.Error())
		}
	})

	t.Run("linear without project_slug", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "codex app-server",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "tracker.linear.project_slug") {
			t.Errorf("error %q should mention tracker.linear.project_slug", err.Error())
		}
	})

	t.Run("plane without workspace_slug", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "plane",
				APIKey: "some_key",
				Plane: PlaneConfig{
					ProjectID: "proj-1",
				},
			},
			Agent: AgentConfig{
				Kind:   "claude",
				Claude: ClaudeConfig{Command: "claude"},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "tracker.plane.workspace_slug") {
			t.Errorf("error %q should mention tracker.plane.workspace_slug", err.Error())
		}
	})

	t.Run("plane without project_id", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "plane",
				APIKey: "some_key",
				Plane: PlaneConfig{
					WorkspaceSlug: "my-ws",
				},
			},
			Agent: AgentConfig{
				Kind:   "claude",
				Claude: ClaudeConfig{Command: "claude"},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "tracker.plane.project_id") {
			t.Errorf("error %q should mention tracker.plane.project_id", err.Error())
		}
	})

	t.Run("codex without command", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
			Agent: AgentConfig{
				Kind: "codex",
				Codex: CodexConfig{
					Command: "",
				},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "agent.codex.command") {
			t.Errorf("error %q should mention agent.codex.command", err.Error())
		}
	})

	t.Run("claude without command", func(t *testing.T) {
		s := &Schema{
			Tracker: TrackerConfig{
				Kind:   "linear",
				APIKey: "some_key",
				Linear: LinearConfig{
					ProjectSlug: "my-project",
				},
			},
			Agent: AgentConfig{
				Kind:   "claude",
				Claude: ClaudeConfig{Command: ""},
			},
		}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "agent.claude.command") {
			t.Errorf("error %q should mention agent.claude.command", err.Error())
		}
	})

	t.Run("multiple errors collected", func(t *testing.T) {
		s := &Schema{}

		err := Validate(s)
		if err == nil {
			t.Fatal("Validate() expected error, got nil")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "tracker.kind") {
			t.Error("error should mention tracker.kind")
		}
		if !strings.Contains(errMsg, "agent.kind") {
			t.Error("error should mention agent.kind")
		}
		if !strings.Contains(errMsg, "tracker.api_key") {
			t.Error("error should mention tracker.api_key")
		}
	})
}
