package agent

import "time"

// EventType enumerates the kinds of events an agent session can emit.
type EventType string

const (
	EventTurnCompleted        EventType = "turn_completed"
	EventTurnFailed           EventType = "turn_failed"
	EventTurnCancelled        EventType = "turn_cancelled"
	EventTurnInputRequired    EventType = "turn_input_required"
	EventApprovalAutoApproved EventType = "approval_auto_approved"
	EventNotification         EventType = "notification"
	EventUsageReport          EventType = "usage_report"
)

// Event represents a single event emitted during an agent session.
type Event struct {
	Type       EventType
	IssueID    string
	SessionID  string
	Message    string
	Usage      *UsageReport
	RateLimits map[string]any // latest rate-limit payload from agent
	Timestamp  time.Time
}

// UsageReport tracks token consumption for an agent turn.
type UsageReport struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}
