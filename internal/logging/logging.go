// Package logging provides structured logging helpers with issue and session context.
package logging

import "log/slog"

// WithIssue returns a logger with issue context attributes.
func WithIssue(issueID, identifier string) *slog.Logger {
	return slog.Default().With(
		"issue_id", issueID,
		"issue_identifier", identifier,
	)
}

// WithSession returns a logger with session context attributes.
func WithSession(sessionID string) *slog.Logger {
	return slog.Default().With(
		"session_id", sessionID,
	)
}

// WithIssueAndSession returns a logger with both issue and session context.
func WithIssueAndSession(issueID, identifier, sessionID string) *slog.Logger {
	return slog.Default().With(
		"issue_id", issueID,
		"issue_identifier", identifier,
		"session_id", sessionID,
	)
}
