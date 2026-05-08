package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/ha"
	"github.com/ainative/go-symphony/internal/orchestrator"
)

// StateProvider provides read-only access to orchestrator state for the HTTP server.
// Defined here because the server consumes it — interfaces belong where they are used.
type StateProvider interface {
	// Running returns a snapshot of currently running issues.
	Running() map[string]*orchestrator.RunInfo

	// RunningCount returns the number of currently running issues.
	RunningCount() int

	// RunningIssue returns the RunInfo for a specific issue, or nil if not running.
	RunningIssue(issueID string) *orchestrator.RunInfo

	// PendingRetries returns all pending retry entries.
	PendingRetries() map[string]*orchestrator.RetryEntry

	// RetryCount returns the number of pending retries.
	RetryCount() int

	// TotalRuntimeSeconds returns aggregate runtime in seconds.
	TotalRuntimeSeconds() float64

	// RateLimits returns the latest rate-limit payload.
	RateLimits() map[string]any
}

// Refresher triggers an immediate orchestrator poll cycle.
type Refresher interface {
	// ForceRefresh triggers an immediate poll cycle.
	ForceRefresh(ctx context.Context)
}

// Server is the HTTP API server for the Symphony dashboard.
type Server struct {
	state         StateProvider
	elector       ha.Elector
	refresh       Refresher
	cfg           config.ServerConfig
	maxConcurrent int
	http          *http.Server
}

// New creates a new Server with the given dependencies.
func New(state StateProvider, elector ha.Elector, refresh Refresher, cfg config.ServerConfig, maxConcurrent int) *Server {
	s := &Server{
		state:         state,
		elector:       elector,
		refresh:       refresh,
		cfg:           cfg,
		maxConcurrent: maxConcurrent,
	}

	addr := fmt.Sprintf("%s:%d", hostOrDefault(cfg.Host), portOrDefault(cfg.Port))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /", s.handleDashboard)

	// API endpoints with CORS support.
	mux.HandleFunc("GET /api/v1/state", s.cors(s.handleState))
	mux.HandleFunc("OPTIONS /api/v1/state", s.cors(s.handleOptions))
	mux.HandleFunc("GET /api/v1/issues/{identifier}", s.cors(s.handleIssue))
	mux.HandleFunc("OPTIONS /api/v1/issues/{identifier}", s.cors(s.handleOptions))
	mux.HandleFunc("POST /api/v1/refresh", s.cors(s.handleRefresh))
	mux.HandleFunc("OPTIONS /api/v1/refresh", s.cors(s.handleOptions))
	mux.HandleFunc("GET /api/v1/events", s.cors(s.handleEvents))
	mux.HandleFunc("OPTIONS /api/v1/events", s.cors(s.handleOptions))

	s.http = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// ListenAndServe starts the HTTP server. It blocks until the server exits
// or the context is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("httpserver: starting", "addr", s.http.Addr)
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("httpserver: listen and serve: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		slog.Info("httpserver: context cancelled, shutting down")
		return s.Shutdown(ctx)
	case err := <-errCh:
		return err
	}
}

// Shutdown performs a graceful shutdown of the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("httpserver: shutting down")
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}
	return nil
}

// Addr returns the address the server is configured to listen on.
func (s *Server) Addr() string {
	return s.http.Addr
}

// cors wraps a handler with CORS headers for API endpoints.
func (s *Server) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		next(w, r)
	}
}

// handleOptions responds to CORS preflight requests.
func (s *Server) handleOptions(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// isLeader returns true if this instance is the leader (or HA is not enabled).
func (s *Server) isLeader() bool {
	if s.elector == nil {
		return true
	}
	return s.elector.IsLeader()
}

func hostOrDefault(host string) string {
	if host == "" {
		return "0.0.0.0"
	}
	return host
}

func portOrDefault(port int) int {
	if port <= 0 {
		return 8080
	}
	return port
}
