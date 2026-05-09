package plane

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// Compile-time interface checks.
var (
	_ tracker.Tracker           = (*Adapter)(nil)
	_ tracker.RawClientProvider = (*Adapter)(nil)
)

// Adapter implements tracker.Tracker for Plane's REST API.
type Adapter struct {
	client         *Client
	activeStates   []string // state groups considered active (e.g., "started")
	terminalStates []string // state groups considered terminal (e.g., "completed", "cancelled")
	stateNameCache map[string]string
}

// NewAdapter creates a Plane tracker adapter from a config map.
// Expected keys: endpoint, api_key, workspace_slug, project_id,
// active_states (optional []string), terminal_states (optional []string).
func NewAdapter(cfg map[string]any) (tracker.Tracker, error) {
	endpoint := strVal(cfg["endpoint"])
	apiKey := strVal(cfg["api_key"])
	workspaceSlug := strVal(cfg["workspace_slug"])
	projectID := strVal(cfg["project_id"])

	if apiKey == "" {
		return nil, tracker.NewTrackerError(tracker.ErrMissingTrackerAPIKey, "plane adapter: api_key is required", nil)
	}
	if workspaceSlug == "" {
		return nil, tracker.NewTrackerError(tracker.ErrMissingTrackerProject, "plane adapter: workspace_slug is required", nil)
	}
	if projectID == "" {
		return nil, tracker.NewTrackerError(tracker.ErrMissingTrackerProject, "plane adapter: project_id is required", nil)
	}

	activeStates := toStringSlice(cfg["active_states"])
	terminalStates := toStringSlice(cfg["terminal_states"])

	// Default active state groups if not specified
	if len(activeStates) == 0 {
		activeStates = []string{"started"}
	}
	// Default terminal state groups if not specified
	if len(terminalStates) == 0 {
		terminalStates = []string{"completed", "cancelled"}
	}

	return &Adapter{
		client:         NewClient(endpoint, apiKey, workspaceSlug, projectID),
		activeStates:   activeStates,
		terminalStates: terminalStates,
		stateNameCache: make(map[string]string),
	}, nil
}

// Register registers the Plane adapter factory in the tracker registry.
func Register() {
	tracker.RegisterTracker("plane", NewAdapter)
}

// FetchCandidateIssues fetches issues in active state groups and normalizes them.
func (a *Adapter) FetchCandidateIssues(ctx context.Context) ([]tracker.Issue, error) {
	// Load states for name resolution
	if err := a.loadStates(ctx); err != nil {
		return nil, fmt.Errorf("fetch candidate issues: %w", err)
	}

	issues, err := a.client.FetchIssues(ctx, a.activeStates)
	if err != nil {
		return nil, fmt.Errorf("fetch candidate issues: %w", err)
	}

	result := make([]tracker.Issue, 0, len(issues))
	for _, pi := range issues {
		issue := a.normalizeIssue(pi)
		result = append(result, issue)
	}
	return result, nil
}

// FetchIssuesByStates fetches all issues and filters by state names.
func (a *Adapter) FetchIssuesByStates(ctx context.Context, states []string) ([]tracker.Issue, error) {
	if err := a.loadStates(ctx); err != nil {
		return nil, fmt.Errorf("fetch issues by states: %w", err)
	}

	// Fetch all issues (no state group filter)
	issues, err := a.client.FetchIssues(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch issues by states: %w", err)
	}

	stateSet := make(map[string]bool, len(states))
	for _, s := range states {
		stateSet[strings.ToLower(s)] = true
	}

	result := make([]tracker.Issue, 0)
	for _, pi := range issues {
		stateName := a.stateNameCache[pi.State]
		if stateSet[strings.ToLower(stateName)] {
			result = append(result, a.normalizeIssue(pi))
		}
	}
	return result, nil
}

// FetchIssueStatesByIDs fetches specific issues by their IDs.
func (a *Adapter) FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]tracker.Issue, error) {
	if err := a.loadStates(ctx); err != nil {
		return nil, fmt.Errorf("fetch issue states by IDs: %w", err)
	}

	result := make([]tracker.Issue, 0, len(ids))
	for _, id := range ids {
		pi, err := a.client.FetchIssueByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("fetch issue state for %s: %w", id, err)
		}
		result = append(result, a.normalizeIssue(*pi))
	}
	return result, nil
}

// CreateComment posts an HTML comment to the specified issue.
func (a *Adapter) CreateComment(ctx context.Context, issueID, body string) error {
	// Wrap plain text in paragraph tags for HTML
	commentHTML := body
	if !strings.Contains(body, "<") {
		commentHTML = "<p>" + html.EscapeString(body) + "</p>"
	}
	return a.client.CreateComment(ctx, issueID, commentHTML)
}

// UpdateIssueState transitions an issue to a new state.
// The state parameter can be either a state UUID or a state name.
func (a *Adapter) UpdateIssueState(ctx context.Context, issueID, state string) error {
	if err := a.loadStates(ctx); err != nil {
		return fmt.Errorf("update issue state: %w", err)
	}

	// Resolve state name to UUID if needed
	stateUUID := state
	for id, name := range a.stateNameCache {
		if strings.EqualFold(name, state) {
			stateUUID = id
			break
		}
	}

	return a.client.UpdateIssueState(ctx, issueID, stateUUID)
}

// RawClient returns the underlying Plane REST client.
func (a *Adapter) RawClient() any {
	return a.client
}

// loadStates fetches and caches state ID-to-name mappings.
func (a *Adapter) loadStates(ctx context.Context) error {
	if len(a.stateNameCache) > 0 {
		return nil
	}

	states, err := a.client.FetchStates(ctx)
	if err != nil {
		return fmt.Errorf("load states: %w", err)
	}

	a.stateNameCache = make(map[string]string, len(states))
	for _, s := range states {
		a.stateNameCache[s.ID] = s.Name
	}
	return nil
}

// normalizeIssue converts a planeIssue to a tracker.Issue.
func (a *Adapter) normalizeIssue(pi planeIssue) tracker.Issue {
	// State name from cache, or fall back to UUID
	stateName := a.stateNameCache[pi.State]
	if stateName == "" {
		stateName = pi.State
	}

	// Identifier: "{project}-{sequence_id}"
	identifier := fmt.Sprintf("%s-%d", a.client.projectID, pi.SequenceID)

	// Strip HTML tags from description
	description := stripHTML(pi.DescriptionHTML)

	// Priority mapping: Plane urgency → int
	priority := mapPriority(pi.Priority)

	// Labels: lowercase
	labels := make([]string, len(pi.Labels))
	for i, l := range pi.Labels {
		labels[i] = strings.ToLower(l)
	}

	// URL construction
	url := fmt.Sprintf("https://app.plane.so/%s/projects/%s/issues/%s",
		a.client.workspaceSlug, a.client.projectID, pi.ID)

	// Parse timestamps
	createdAt := parseTime(pi.CreatedAt)
	updatedAt := parseTime(pi.UpdatedAt)

	return tracker.Issue{
		ID:          pi.ID,
		Identifier:  identifier,
		Title:       pi.Name,
		Description: description,
		State:       stateName,
		Priority:    priority,
		Labels:      labels,
		URL:         url,
		BranchName:  pi.BranchName,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
}

// mapPriority converts Plane priority (0-4) to a *int.
// Plane: urgent=1, high=2, medium=3, low=4, none=0
func mapPriority(p int) *int {
	switch p {
	case 0: // none
		return nil
	default:
		return &p
	}
}

// stripHTML removes HTML tags and unescapes entities.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	// Collapse multiple whitespace
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}

// parseTime parses an ISO 8601 timestamp string.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Try common ISO 8601 formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999Z",
		"2006-01-02T15:04:05Z",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// toStringSlice extracts a []string from a map value.
func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		result := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}
