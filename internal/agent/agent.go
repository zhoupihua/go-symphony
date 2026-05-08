package agent

import (
	"context"
	"time"

	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// SessionOptions configures a new agent session.
type SessionOptions struct {
	WorkspacePath string            // Root directory for the workspace
	WorkerHost    string            // Empty = local, non-empty = SSH remote host
	Tracker       tracker.Tracker   // Tracker instance for dynamic tools
	Issue         tracker.Issue     // The issue this session is working on
	Config        map[string]any    // Adapter-specific configuration
}

// TurnOptions configures a single agent turn.
type TurnOptions struct {
	MaxTurns       int
	Timeout        time.Duration
	ApprovalPolicy string
}

// TurnResult is the outcome of a single agent turn.
type TurnResult struct {
	Completed  bool
	Usage      UsageReport
	Output     string
	SessionID  string         // composed session identifier (e.g. "<thread_id>-<turn_id>")
	RateLimits map[string]any // rate-limit info from agent, if any
}

// Agent creates and manages sessions. Adapters implement this interface.
type Agent interface {
	// StartSession creates a new session for the given issue and workspace.
	StartSession(ctx context.Context, opts SessionOptions) (Session, error)
}

// Session represents an active agent conversation. Adapters implement this interface.
type Session interface {
	// RunTurn executes one turn of the agent with the given prompt.
	RunTurn(ctx context.Context, prompt string, opts TurnOptions) (TurnResult, error)

	// Close releases session resources.
	Close() error
}
