package linear

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ainative/go-symphony/internal/tracker"
)

// Compile-time interface checks.
var (
	_ tracker.Tracker           = (*Adapter)(nil)
	_ tracker.RawClientProvider = (*Adapter)(nil)
)

// Adapter implements tracker.Tracker for the Linear issue tracker.
type Adapter struct {
	client          *Client
	activeStates    []string
	terminalStates  []string
}

// NewAdapter creates a Linear adapter from a config map.
// Expected config keys: endpoint, api_key, project_slug, active_states, terminal_states.
func NewAdapter(cfg map[string]any) (tracker.Tracker, error) {
	endpoint := strVal(cfg["endpoint"])
	apiKey := strVal(cfg["api_key"])
	projectSlug := strVal(cfg["project_slug"])

	if apiKey == "" {
		return nil, fmt.Errorf("linear adapter: api_key is required")
	}
	if projectSlug == "" {
		return nil, fmt.Errorf("linear adapter: project_slug is required")
	}

	var activeStates []string
	if v := cfg["active_states"]; v != nil {
		activeStates = strSliceVal(v)
	}

	var terminalStates []string
	if v := cfg["terminal_states"]; v != nil {
		terminalStates = strSliceVal(v)
	}

	client := NewClient(endpoint, apiKey, projectSlug)

	return &Adapter{
		client:         client,
		activeStates:   activeStates,
		terminalStates: terminalStates,
	}, nil
}

// Register registers the Linear adapter with the tracker registry.
func Register() {
	tracker.RegisterTracker("linear", NewAdapter)
}

// FetchCandidateIssues returns issues in active states for the configured project.
func (a *Adapter) FetchCandidateIssues(ctx context.Context) ([]tracker.Issue, error) {
	filter := map[string]any{
		"stateNames": a.activeStates,
	}

	rawIssues, err := a.client.FetchIssues(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("fetch candidate issues: %w", err)
	}

	return normalizeIssues(rawIssues), nil
}

// FetchIssuesByStates returns issues matching any of the given states.
func (a *Adapter) FetchIssuesByStates(ctx context.Context, states []string) ([]tracker.Issue, error) {
	if len(states) == 0 {
		return []tracker.Issue{}, nil
	}

	filter := map[string]any{
		"stateNames": states,
	}

	rawIssues, err := a.client.FetchIssues(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("fetch issues by states: %w", err)
	}

	return normalizeIssues(rawIssues), nil
}

// FetchIssueStatesByIDs returns issues with current state for the given IDs.
func (a *Adapter) FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]tracker.Issue, error) {
	if len(ids) == 0 {
		return []tracker.Issue{}, nil
	}

	// Deduplicate IDs
	seen := make(map[string]bool, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	// Preserve order index for sorting
	orderIndex := make(map[string]int, len(uniqueIDs))
	for i, id := range uniqueIDs {
		orderIndex[id] = i
	}

	rawIssues, err := a.client.FetchIssuesByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch issue states by ids: %w", err)
	}

	issues := normalizeIssues(rawIssues)

	// Sort by requested ID order
	sortIssuesByRequestedIDs(issues, orderIndex)

	return issues, nil
}

// CreateComment adds a comment to the specified issue.
func (a *Adapter) CreateComment(ctx context.Context, issueID, body string) error {
	return a.client.CreateCommentMutation(ctx, issueID, body)
}

// UpdateIssueState transitions an issue to a new state.
func (a *Adapter) UpdateIssueState(ctx context.Context, issueID, state string) error {
	return a.client.UpdateIssueStateMutation(ctx, issueID, state)
}

// RawClient returns the underlying Linear GraphQL client.
func (a *Adapter) RawClient() any {
	return a.client
}

// normalizeIssues converts a slice of raw Linear issues to tracker.Issue values.
func normalizeIssues(raw []linearIssue) []tracker.Issue {
	if len(raw) == 0 {
		return []tracker.Issue{}
	}

	issues := make([]tracker.Issue, 0, len(raw))
	for _, ri := range raw {
		issue := tracker.Issue{
			ID:          ri.ID,
			Identifier:  ri.Identifier,
			Title:       ri.Title,
			Description: ri.Description,
			Priority:    ri.Priority,
			State:       ri.State.Name,
			URL:         ri.URL,
			BranchName:  ri.BranchName,
			Labels:      normalizeLabels(ri),
			BlockedBy:   extractBlockedBy(ri),
			CreatedAt:   parseTime(ri.CreatedAt),
			UpdatedAt:   parseTime(ri.UpdatedAt),
		}
		issues = append(issues, issue)
	}
	return issues
}

// normalizeLabels extracts and lowercases label names.
func normalizeLabels(ri linearIssue) []string {
	labels := make([]string, 0, len(ri.Labels.Nodes))
	for _, node := range ri.Labels.Nodes {
		if node.Name != "" {
			labels = append(labels, strings.ToLower(node.Name))
		}
	}
	return labels
}

// extractBlockedBy returns BlockerRefs for issues that block this one,
// derived from inverse relations of type "blocks".
func extractBlockedBy(ri linearIssue) []tracker.BlockerRef {
	var blocked []tracker.BlockerRef
	for _, rel := range ri.InverseRelations.Nodes {
		if strings.ToLower(strings.TrimSpace(rel.Type)) == "blocks" {
			if rel.Issue.Identifier != "" {
				blocked = append(blocked, tracker.BlockerRef{
					ID:         rel.Issue.ID,
					Identifier: rel.Issue.Identifier,
					State:      rel.Issue.State.Name,
				})
			}
		}
	}
	return blocked
}

// parseTime parses an ISO-8601 timestamp string.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try without timezone (Linear sometimes omits it)
		t, err = time.Parse("2006-01-02T15:04:05", s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

// sortIssuesByRequestedIDs sorts issues to match the order of the requested IDs.
func sortIssuesByRequestedIDs(issues []tracker.Issue, orderIndex map[string]int) {
	fallback := len(orderIndex)
	for i := 1; i < len(issues); i++ {
		for j := i; j > 0; j-- {
			jIdx := orderIndex[issues[j].ID]
			if jIdx == 0 && issues[j].ID != "" {
				if _, exists := orderIndex[issues[j].ID]; !exists {
					jIdx = fallback
				}
			}
			jm1Idx := orderIndex[issues[j-1].ID]
			if jm1Idx == 0 && issues[j-1].ID != "" {
				if _, exists := orderIndex[issues[j-1].ID]; !exists {
					jm1Idx = fallback
				}
			}
			if jIdx < jm1Idx {
				issues[j], issues[j-1] = issues[j-1], issues[j]
			} else {
				break
			}
		}
	}
}

// strVal extracts a string value from a map[string]any entry.
func strVal(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// strSliceVal extracts a []string from a map[string]any entry.
// Handles both []string and []any types.
func strSliceVal(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}
