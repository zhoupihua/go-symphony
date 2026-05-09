package tracker

import "fmt"

// Error categories per SPEC §11.4.
const (
	ErrUnsupportedTrackerKind = "unsupported_tracker_kind"
	ErrMissingTrackerAPIKey   = "missing_tracker_api_key"
	ErrMissingTrackerProject  = "missing_tracker_project"
	ErrAPIRequest             = "api_request"
	ErrAPIStatus              = "api_status"
	ErrGraphQLErrors          = "graphql_errors"
	ErrUnknownPayload         = "unknown_payload"
	ErrMissingEndCursor       = "missing_end_cursor"
)

// TrackerError is a categorized error from a tracker adapter.
type TrackerError struct {
	Category string
	Message  string
	Cause    error
}

func (e *TrackerError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Category, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func (e *TrackerError) Unwrap() error {
	return e.Cause
}

// NewTrackerError creates a categorized tracker error.
func NewTrackerError(category, message string, cause error) *TrackerError {
	return &TrackerError{Category: category, Message: message, Cause: cause}
}
