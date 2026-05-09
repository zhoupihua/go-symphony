package agent

import "fmt"

// Error categories per SPEC §10.6.
const (
	ErrCodexNotFound       = "codex_not_found"
	ErrInvalidWorkspaceCWD = "invalid_workspace_cwd"
	ErrResponseTimeout     = "response_timeout"
	ErrTurnTimeout         = "turn_timeout"
	ErrPortExit            = "port_exit"
	ErrResponseError       = "response_error"
	ErrTurnFailed          = "turn_failed"
	ErrTurnCancelled       = "turn_cancelled"
	ErrTurnInputRequired   = "turn_input_required"
	ErrSessionStartup      = "session_startup"
	ErrStallDetected       = "stall_detected"
)

// AgentError is a categorized error from the agent runner.
type AgentError struct {
	Category string
	Message  string
	Cause    error
}

func (e *AgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Category, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func (e *AgentError) Unwrap() error {
	return e.Cause
}

// NewAgentError creates a categorized agent error.
func NewAgentError(category, message string, cause error) *AgentError {
	return &AgentError{Category: category, Message: message, Cause: cause}
}
