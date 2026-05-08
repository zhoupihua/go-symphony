package plane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps HTTP requests to Plane's REST API.
type Client struct {
	baseURL       string
	apiKey        string
	workspaceSlug string
	projectID     string
	http          *http.Client
}

// NewClient creates a new Plane API client.
// If baseURL is empty, it defaults to "https://api.plane.so/api/".
func NewClient(baseURL, apiKey, workspaceSlug, projectID string) *Client {
	if baseURL == "" {
		baseURL = "https://api.plane.so/api/"
	}
	return &Client{
		baseURL:       baseURL,
		apiKey:        apiKey,
		workspaceSlug: workspaceSlug,
		projectID:     projectID,
		http:          &http.Client{Timeout: 30 * time.Second},
	}
}

// planeState represents a state object from Plane's API.
type planeState struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// planeIssue represents an issue object from Plane's API.
type planeIssue struct {
	ID             string   `json:"id"`
	SequenceID     int      `json:"sequence_id"`
	Name           string   `json:"name"`
	DescriptionHTML string  `json:"description_html"`
	State          string   `json:"state"`
	Priority       int      `json:"priority"`
	Labels         []string `json:"labels"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

// Get sends a GET request to the given path (relative to baseURL).
func (c *Client) Get(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode GET %s response: %w", path, err)
	}
	return result, nil
}

// Post sends a POST request with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body any) (map[string]any, error) {
	return c.doWithBody(ctx, http.MethodPost, path, body)
}

// Patch sends a PATCH request with a JSON body.
func (c *Client) Patch(ctx context.Context, path string, body any) (map[string]any, error) {
	return c.doWithBody(ctx, http.MethodPatch, path, body)
}

// doWithBody sends a request with a JSON body.
func (c *Client) doWithBody(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal %s body: %w", method, err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return result, nil
}

// FetchStates retrieves all states for the project.
func (c *Client) FetchStates(ctx context.Context) ([]planeState, error) {
	path := fmt.Sprintf("workspaces/%s/projects/%s/states/", c.workspaceSlug, c.projectID)
	result, err := c.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fetch states: %w", err)
	}

	statesRaw, ok := result["results"]
	if !ok {
		// Might be a single-page response without "results" wrapper
		return nil, fmt.Errorf("fetch states: unexpected response format")
	}

	statesList, ok := statesRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("fetch states: results is not a list")
	}

	states := make([]planeState, 0, len(statesList))
	for _, raw := range statesList {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		s := planeState{
			ID:    strVal(m["id"]),
			Name:  strVal(m["name"]),
			Group: strVal(m["group"]),
		}
		states = append(states, s)
	}
	return states, nil
}

// FetchIssues retrieves issues with offset/limit pagination, optionally filtering by state groups.
func (c *Client) FetchIssues(ctx context.Context, stateGroups []string) ([]planeIssue, error) {
	path := fmt.Sprintf("workspaces/%s/projects/%s/issues/", c.workspaceSlug, c.projectID)

	groupSet := make(map[string]bool, len(stateGroups))
	for _, g := range stateGroups {
		groupSet[g] = true
	}

	var allIssues []planeIssue
	offset := 0
	limit := 50

	for {
		paginatedPath := fmt.Sprintf("%s?offset=%d&limit=%d", path, offset, limit)
		result, err := c.Get(ctx, paginatedPath)
		if err != nil {
			return nil, fmt.Errorf("fetch issues at offset %d: %w", offset, err)
		}

		resultsRaw, ok := result["results"]
		if !ok {
			return nil, fmt.Errorf("fetch issues: unexpected response format")
		}

		resultsList, ok := resultsRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("fetch issues: results is not a list")
		}

		if len(resultsList) == 0 {
			break
		}

		for _, raw := range resultsList {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			issue := parsePlaneIssue(m)
			allIssues = append(allIssues, issue)
		}

		if len(resultsList) < limit {
			break
		}
		offset += limit
	}

	// If state groups specified, we need states to filter.
	// But the API doesn't support filtering by state group directly,
	// so we filter client-side.
	if len(stateGroups) > 0 {
		states, err := c.FetchStates(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch states for filtering: %w", err)
		}
		stateIDToGroup := make(map[string]string, len(states))
		for _, s := range states {
			stateIDToGroup[s.ID] = s.Group
		}

		filtered := allIssues[:0]
		for _, issue := range allIssues {
			if group, ok := stateIDToGroup[issue.State]; ok && groupSet[group] {
				filtered = append(filtered, issue)
			}
		}
		allIssues = filtered
	}

	return allIssues, nil
}

// FetchIssueByID retrieves a single issue by ID.
func (c *Client) FetchIssueByID(ctx context.Context, issueID string) (*planeIssue, error) {
	path := fmt.Sprintf("workspaces/%s/projects/%s/issues/%s/", c.workspaceSlug, c.projectID, issueID)
	result, err := c.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fetch issue %s: %w", issueID, err)
	}
	issue := parsePlaneIssue(result)
	return &issue, nil
}

// CreateComment posts a comment to an issue.
func (c *Client) CreateComment(ctx context.Context, issueID, commentHTML string) error {
	path := fmt.Sprintf("workspaces/%s/projects/%s/issues/%s/comments/", c.workspaceSlug, c.projectID, issueID)
	body := map[string]string{"comment_html": commentHTML}
	_, err := c.Post(ctx, path, body)
	if err != nil {
		return fmt.Errorf("create comment on issue %s: %w", issueID, err)
	}
	return nil
}

// UpdateIssueState patches the state of an issue.
func (c *Client) UpdateIssueState(ctx context.Context, issueID, stateUUID string) error {
	path := fmt.Sprintf("workspaces/%s/projects/%s/issues/%s/", c.workspaceSlug, c.projectID, issueID)
	body := map[string]string{"state": stateUUID}
	_, err := c.Patch(ctx, path, body)
	if err != nil {
		return fmt.Errorf("update issue %s state: %w", issueID, err)
	}
	return nil
}

// checkStatus validates the HTTP response status code.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// strVal extracts a string value from a map[string]any, returning "" for non-strings.
func strVal(v any) string {
	s, _ := v.(string)
	return s
}

// intVal extracts an int value from a map[string]any, returning 0 for non-numbers.
func intVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// parsePlaneIssue builds a planeIssue from a raw map.
func parsePlaneIssue(m map[string]any) planeIssue {
	var labels []string
	if raw, ok := m["labels"].([]any); ok {
		for _, l := range raw {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			}
		}
	}

	return planeIssue{
		ID:              strVal(m["id"]),
		SequenceID:      intVal(m["sequence_id"]),
		Name:            strVal(m["name"]),
		DescriptionHTML: strVal(m["description_html"]),
		State:           strVal(m["state"]),
		Priority:        intVal(m["priority"]),
		Labels:          labels,
		CreatedAt:       strVal(m["created_at"]),
		UpdatedAt:       strVal(m["updated_at"]),
	}
}
