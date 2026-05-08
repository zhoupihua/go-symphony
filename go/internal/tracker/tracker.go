package tracker

import "context"

// Tracker is the interface that issue tracker adapters must implement.
type Tracker interface {
	// FetchCandidateIssues returns issues eligible for processing.
	FetchCandidateIssues(ctx context.Context) ([]Issue, error)

	// FetchIssuesByStates returns issues matching any of the given states.
	FetchIssuesByStates(ctx context.Context, states []string) ([]Issue, error)

	// FetchIssueStatesByIDs returns issues with current state for the given IDs.
	FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]Issue, error)

	// CreateComment adds a comment to the specified issue.
	CreateComment(ctx context.Context, issueID, body string) error

	// UpdateIssueState transitions an issue to a new state.
	UpdateIssueState(ctx context.Context, issueID, state string) error
}

// RawClientProvider is an optional interface that Tracker adapters can implement
// to expose their underlying API client for use by dynamic tools (e.g., Codex's
// linear_graphql and plane_rest tools).
type RawClientProvider interface {
	// RawClient returns the underlying API client.
	// The return type is interface{} because each tracker has a different client type.
	// Callers should type-assert to the expected client type.
	RawClient() any
}
