package tracker

import "time"

// Issue represents a work item from an issue tracker.
type Issue struct {
	ID          string   // Tracker-specific ID
	Identifier  string   // Human-readable (e.g., "ENG-123")
	Title       string
	Description string
	State       string
	Priority    *int     // nil if not set
	Labels      []string
	URL         string
	BlockedBy   []string // Issue identifiers blocking this one
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
