package tracker

import "time"

// BlockerRef represents an issue that blocks another issue.
type BlockerRef struct {
	ID         string // Tracker-specific ID of the blocker
	Identifier string // Human-readable key (e.g., "ENG-123")
	State      string // Current state of the blocker (empty if unknown)
}

// Issue represents a work item from an issue tracker.
type Issue struct {
	ID          string       // Tracker-specific ID
	Identifier  string       // Human-readable (e.g., "ENG-123")
	Title       string
	Description string
	State       string
	Priority    *int         // nil if not set
	Labels      []string
	URL         string
	BlockedBy   []BlockerRef // Issues blocking this one
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
