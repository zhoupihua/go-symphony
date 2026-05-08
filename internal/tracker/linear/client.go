package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultEndpoint = "https://api.linear.app/graphql"
	pageSize        = 50
)

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the top-level response from a GraphQL endpoint.
type graphqlResponse struct {
	Data   map[string]any `json:"data,omitempty"`
	Errors []graphqlError `json:"errors,omitempty"`
}

// graphqlError represents a single error from the GraphQL response.
type graphqlError struct {
	Message string `json:"message"`
}

// linearIssue represents a raw issue node from the Linear GraphQL API.
type linearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    *int   `json:"priority"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	URL        string `json:"url"`
	BranchName string `json:"branchName"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	InverseRelations struct {
		Nodes []struct {
			Type  string `json:"type"`
			Issue struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
				State      struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"issue"`
		} `json:"nodes"`
	} `json:"inverseRelations"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// pageInfo represents pagination information from a GraphQL connection.
type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

// Client wraps HTTP POST to Linear's GraphQL endpoint.
type Client struct {
	endpoint string
	apiKey   string
	project  string
	http     *http.Client
}

// NewClient creates a new Linear GraphQL client.
// If endpoint is empty, the default "https://api.linear.app/graphql" is used.
func NewClient(endpoint, apiKey, projectSlug string) *Client {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	return &Client{
		endpoint: endpoint,
		apiKey:   apiKey,
		project:  projectSlug,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Query sends a GraphQL request and returns the response data.
func (c *Client) Query(ctx context.Context, query string, variables map[string]any) (map[string]any, error) {
	reqBody := graphqlRequest{
		Query:     query,
		Variables: variables,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create graphql request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Error("Linear GraphQL request failed",
			slog.Int("status", resp.StatusCode),
			slog.String("body", truncate(string(respBody), 1000)),
		)
		return nil, fmt.Errorf("linear api status %d", resp.StatusCode)
	}

	var gqlResp graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

// FetchIssues retrieves all issues for the configured project with pagination.
// The filter parameter is merged into the GraphQL variables for the issues query.
func (c *Client) FetchIssues(ctx context.Context, filter map[string]any) ([]linearIssue, error) {
	query := `
query SymphonyLinearPoll($projectSlug: String!, $stateNames: [String!]!, $first: Int!, $relationFirst: Int!, $after: String) {
  issues(filter: {project: {slugId: {eq: $projectSlug}}, state: {name: {in: $stateNames}}}, first: $first, after: $after) {
    nodes {
      id
      identifier
      title
      description
      priority
      state { name }
      url
      labels { nodes { name } }
      inverseRelations(first: $relationFirst) {
        nodes {
          type
          issue {
            id
            identifier
            state { name }
          }
        }
      }
      createdAt
      updatedAt
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

	variables := map[string]any{
		"projectSlug":   c.project,
		"stateNames":    filter["stateNames"],
		"first":         pageSize,
		"relationFirst": pageSize,
	}

	var allIssues []linearIssue
	afterCursor := ""

	for {
		if afterCursor != "" {
			variables["after"] = afterCursor
		} else {
			delete(variables, "after")
		}

		data, err := c.Query(ctx, query, variables)
		if err != nil {
			return nil, fmt.Errorf("fetch issues page: %w", err)
		}

		issuesData, ok := data["issues"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected issues response format")
		}

		// Decode nodes
		nodesRaw, ok := issuesData["nodes"].([]any)
		if !ok {
			return nil, fmt.Errorf("unexpected nodes format in issues response")
		}

		for _, nodeRaw := range nodesRaw {
			nodeBytes, err := json.Marshal(nodeRaw)
			if err != nil {
				return nil, fmt.Errorf("marshal issue node: %w", err)
			}
			var issue linearIssue
			if err := json.Unmarshal(nodeBytes, &issue); err != nil {
				return nil, fmt.Errorf("unmarshal issue node: %w", err)
			}
			allIssues = append(allIssues, issue)
		}

		// Check pagination
		pageInfoRaw, ok := issuesData["pageInfo"].(map[string]any)
		if !ok {
			break
		}
		pi := pageInfoFromMap(pageInfoRaw)
		if !pi.HasNextPage {
			break
		}
		if pi.EndCursor == "" {
			return nil, fmt.Errorf("linear missing end cursor")
		}
		afterCursor = pi.EndCursor
	}

	return allIssues, nil
}

// FetchIssuesByIDs retrieves issues by their IDs.
func (c *Client) FetchIssuesByIDs(ctx context.Context, ids []string) ([]linearIssue, error) {
	query := `
query SymphonyLinearIssuesById($ids: [ID!]!, $first: Int!, $relationFirst: Int!) {
  issues(filter: {id: {in: $ids}}, first: $first) {
    nodes {
      id
      identifier
      title
      description
      priority
      state { name }
      url
      labels { nodes { name } }
      inverseRelations(first: $relationFirst) {
        nodes {
          type
          issue {
            id
            identifier
            state { name }
          }
        }
      }
      createdAt
      updatedAt
    }
  }
}`

	var allIssues []linearIssue

	// Batch in pages of pageSize
	for batchStart := 0; batchStart < len(ids); batchStart += pageSize {
		batchEnd := min(batchStart+pageSize, len(ids))
		batchIDs := ids[batchStart:batchEnd]

		variables := map[string]any{
			"ids":           batchIDs,
			"first":         len(batchIDs),
			"relationFirst": pageSize,
		}

		data, err := c.Query(ctx, query, variables)
		if err != nil {
			return nil, fmt.Errorf("fetch issues by ids: %w", err)
		}

		issuesData, ok := data["issues"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected issues response format")
		}

		nodesRaw, ok := issuesData["nodes"].([]any)
		if !ok {
			return nil, fmt.Errorf("unexpected nodes format in issues response")
		}

		for _, nodeRaw := range nodesRaw {
			nodeBytes, err := json.Marshal(nodeRaw)
			if err != nil {
				return nil, fmt.Errorf("marshal issue node: %w", err)
			}
			var issue linearIssue
			if err := json.Unmarshal(nodeBytes, &issue); err != nil {
				return nil, fmt.Errorf("unmarshal issue node: %w", err)
			}
			allIssues = append(allIssues, issue)
		}
	}

	return allIssues, nil
}

// CreateCommentMutation sends a GraphQL mutation to create a comment on an issue.
func (c *Client) CreateCommentMutation(ctx context.Context, issueID, body string) error {
	query := `
mutation SymphonyCreateComment($issueId: String!, $body: String!) {
  commentCreate(input: {issueId: $issueId, body: $body}) {
    success
  }
}`

	variables := map[string]any{
		"issueId": issueID,
		"body":    body,
	}

	data, err := c.Query(ctx, query, variables)
	if err != nil {
		return fmt.Errorf("create comment: %w", err)
	}

	createResult, ok := data["commentCreate"].(map[string]any)
	if !ok {
		return fmt.Errorf("comment create failed: unexpected response format")
	}
	success, _ := createResult["success"].(bool)
	if !success {
		return fmt.Errorf("comment create failed")
	}
	return nil
}

// UpdateIssueStateMutation sends a GraphQL mutation to update an issue's state.
// It first resolves the state name to a state ID, then performs the update.
func (c *Client) UpdateIssueStateMutation(ctx context.Context, issueID, stateName string) error {
	// Step 1: Resolve state name to state ID
	stateID, err := c.resolveStateID(ctx, issueID, stateName)
	if err != nil {
		return fmt.Errorf("resolve state id: %w", err)
	}

	// Step 2: Update issue state
	query := `
mutation SymphonyUpdateIssueState($issueId: String!, $stateId: String!) {
  issueUpdate(id: $issueId, input: {stateId: $stateId}) {
    success
  }
}`

	variables := map[string]any{
		"issueId": issueID,
		"stateId": stateID,
	}

	data, err := c.Query(ctx, query, variables)
	if err != nil {
		return fmt.Errorf("update issue state: %w", err)
	}

	updateResult, ok := data["issueUpdate"].(map[string]any)
	if !ok {
		return fmt.Errorf("issue update failed: unexpected response format")
	}
	success, _ := updateResult["success"].(bool)
	if !success {
		return fmt.Errorf("issue update failed")
	}
	return nil
}

// resolveStateID looks up the Linear state ID for a given state name within the issue's team.
func (c *Client) resolveStateID(ctx context.Context, issueID, stateName string) (string, error) {
	query := `
query SymphonyResolveStateId($issueId: String!, $stateName: String!) {
  issue(id: $issueId) {
    team {
      states(filter: {name: {eq: $stateName}}, first: 1) {
        nodes {
          id
        }
      }
    }
  }
}`

	variables := map[string]any{
		"issueId":   issueID,
		"stateName": stateName,
	}

	data, err := c.Query(ctx, query, variables)
	if err != nil {
		return "", fmt.Errorf("resolve state id query: %w", err)
	}

	issueData, ok := data["issue"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("state not found: unexpected response format")
	}
	teamData, ok := issueData["team"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("state not found: team not in response")
	}
	statesData, ok := teamData["states"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("state not found: states not in response")
	}
	nodesRaw, ok := statesData["nodes"].([]any)
	if !ok || len(nodesRaw) == 0 {
		return "", fmt.Errorf("state not found: %s", stateName)
	}
	firstNode, ok := nodesRaw[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("state not found: unexpected node format")
	}
	stateID, ok := firstNode["id"].(string)
	if !ok {
		return "", fmt.Errorf("state not found: id missing in node")
	}
	return stateID, nil
}

// pageInfoFromMap extracts pageInfo from a raw map.
func pageInfoFromMap(m map[string]any) pageInfo {
	var pi pageInfo
	if v, ok := m["hasNextPage"].(bool); ok {
		pi.HasNextPage = v
	}
	if v, ok := m["endCursor"].(string); ok {
		pi.EndCursor = v
	}
	return pi
}

// truncate shortens a string to maxLen bytes, appending "...<truncated>" if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...<truncated>"
}
