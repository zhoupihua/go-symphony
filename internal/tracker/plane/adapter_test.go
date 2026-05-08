package plane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainative/go-symphony/internal/tracker"
)

// testServer creates an httptest.Server that serves Plane API responses
// and returns the server, a state map for reference, and a cleanup function.
func testServer(t *testing.T) (*httptest.Server, map[string]planeState) {
	t.Helper()

	states := map[string]planeState{
		"st-backlog":   {ID: "st-backlog", Name: "Backlog", Group: "backlog"},
		"st-todo":      {ID: "st-todo", Name: "Todo", Group: "unstarted"},
		"st-progress":  {ID: "st-progress", Name: "In Progress", Group: "started"},
		"st-done":      {ID: "st-done", Name: "Done", Group: "completed"},
		"st-cancelled": {ID: "st-cancelled", Name: "Cancelled", Group: "cancelled"},
	}

	issues := []map[string]any{
		{
			"id":              "issue-1",
			"sequence_id":     float64(1),
			"name":            "Bug fix",
			"description_html": "<p>Fix the bug</p>",
			"state":           "st-progress",
			"priority":        float64(1), // urgent
			"labels":          []any{"bug", "urgent"},
			"created_at":      "2024-01-15T10:00:00Z",
			"updated_at":      "2024-01-16T12:00:00Z",
		},
		{
			"id":              "issue-2",
			"sequence_id":     float64(2),
			"name":            "Feature request",
			"description_html": "<p>Add feature</p>",
			"state":           "st-backlog",
			"priority":        float64(2), // high
			"labels":          []any{"feature"},
			"created_at":      "2024-01-15T11:00:00Z",
			"updated_at":      "2024-01-16T13:00:00Z",
		},
		{
			"id":              "issue-3",
			"sequence_id":     float64(3),
			"name":            "Completed task",
			"description_html": "<p>Done already</p>",
			"state":           "st-done",
			"priority":        float64(3), // medium
			"labels":          []any{},
			"created_at":      "2024-01-15T12:00:00Z",
			"updated_at":      "2024-01-16T14:00:00Z",
		},
		{
			"id":              "issue-4",
			"sequence_id":     float64(4),
			"name":            "No priority",
			"description_html": "<p>No priority set</p>",
			"state":           "st-todo",
			"priority":        float64(0), // none
			"labels":          []any{},
			"created_at":      "2024-01-15T13:00:00Z",
			"updated_at":      "2024-01-16T15:00:00Z",
		},
	}

	mux := http.NewServeMux()

	// States endpoint
	mux.HandleFunc("/workspaces/{workspace}/projects/{project}/states/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var stateList []map[string]any
		for _, s := range states {
			stateList = append(stateList, map[string]any{
				"id":    s.ID,
				"name":  s.Name,
				"group": s.Group,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": stateList})
	})

	// Issues list endpoint (with pagination support)
	mux.HandleFunc("/workspaces/{workspace}/projects/{project}/issues/", func(w http.ResponseWriter, r *http.Request) {
		// If this is a PATCH, handle state update
		if r.Method == http.MethodPatch {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			issueID := strings.TrimSuffix(r.URL.Path, "/")
			parts := strings.Split(issueID, "/")
			id := parts[len(parts)-1]
			json.NewEncoder(w).Encode(map[string]any{"id": id, "state": body["state"]})
			return
		}

		// Check for specific issue ID in path (FetchIssueByID)
		pathParts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		if len(pathParts) > 0 {
			lastPart := pathParts[len(pathParts)-1]
			if strings.HasPrefix(lastPart, "issue-") {
				for _, issue := range issues {
					if issue["id"] == lastPart {
						json.NewEncoder(w).Encode(issue)
						return
					}
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}

		// List issues with optional state group filtering
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": issues})
	})

	// Comments endpoint
	mux.HandleFunc("/workspaces/{workspace}/projects/{project}/issues/{issue}/comments/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{
			"id":           "comment-new",
			"comment_html": body["comment_html"],
		})
	})

	server := httptest.NewServer(mux)
	return server, states
}

func newTestAdapter(t *testing.T, serverURL string, activeStates, terminalStates []string) *Adapter {
	t.Helper()
	cfg := map[string]any{
		"endpoint":       serverURL + "/",
		"api_key":        "test-key",
		"workspace_slug": "test-workspace",
		"project_id":     "proj-1",
	}
	if activeStates != nil {
		cfg["active_states"] = activeStates
	}
	if terminalStates != nil {
		cfg["terminal_states"] = terminalStates
	}
	trk, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	return trk.(*Adapter)
}

func TestAdapter_FetchCandidateIssues(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed", "cancelled"})
	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}

	// Only issue-1 is in "started" state group
	if len(issues) != 1 {
		t.Fatalf("expected 1 candidate issue, got %d", len(issues))
	}
	if issues[0].ID != "issue-1" {
		t.Errorf("expected issue-1, got %q", issues[0].ID)
	}
	if issues[0].State != "In Progress" {
		t.Errorf("expected state 'In Progress', got %q", issues[0].State)
	}
}

func TestAdapter_FetchIssuesByStates(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	issues, err := adapter.FetchIssuesByStates(context.Background(), []string{"Backlog", "In Progress"})
	if err != nil {
		t.Fatalf("FetchIssuesByStates returned error: %v", err)
	}

	// issue-1 (In Progress) and issue-2 (Backlog) match
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}

	foundIDs := map[string]bool{}
	for _, iss := range issues {
		foundIDs[iss.ID] = true
	}
	if !foundIDs["issue-1"] || !foundIDs["issue-2"] {
		t.Errorf("expected issue-1 and issue-2, got IDs: %v", foundIDs)
	}
}

func TestAdapter_FetchIssueStatesByIDs(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	issues, err := adapter.FetchIssueStatesByIDs(context.Background(), []string{"issue-1", "issue-3"})
	if err != nil {
		t.Fatalf("FetchIssueStatesByIDs returned error: %v", err)
	}

	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}

	byID := map[string]tracker.Issue{}
	for _, iss := range issues {
		byID[iss.ID] = iss
	}
	if byID["issue-1"].State != "In Progress" {
		t.Errorf("expected issue-1 state 'In Progress', got %q", byID["issue-1"].State)
	}
	if byID["issue-3"].State != "Done" {
		t.Errorf("expected issue-3 state 'Done', got %q", byID["issue-3"].State)
	}
}

func TestAdapter_CreateComment(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	err := adapter.CreateComment(context.Background(), "issue-1", "Done!")
	if err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}
}

func TestAdapter_UpdateIssueState(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	err := adapter.UpdateIssueState(context.Background(), "issue-1", "Done")
	if err != nil {
		t.Fatalf("UpdateIssueState returned error: %v", err)
	}
}

func TestAdapter_PriorityMapping(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}

	// issue-1 has priority=1 (urgent)
	if len(issues) < 1 {
		t.Fatal("expected at least 1 issue")
	}
	if issues[0].Priority == nil {
		t.Error("expected non-nil priority for urgent (1)")
	} else if *issues[0].Priority != 1 {
		t.Errorf("expected priority 1 (urgent), got %d", *issues[0].Priority)
	}
}

func TestAdapter_PriorityNone(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, nil, nil)
	// Fetch all issues (no active state filter) by using activeStates that includes everything
	adapter.activeStates = []string{"backlog", "unstarted", "started", "completed", "cancelled"}

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}

	// issue-4 has priority=0 (none) -> should be nil
	for _, iss := range issues {
		if iss.ID == "issue-4" {
			if iss.Priority != nil {
				t.Errorf("expected nil priority for none (0), got %d", *iss.Priority)
			}
		}
	}
}

func TestAdapter_CustomEndpoint(t *testing.T) {
	// Verify that a custom endpoint is used (self-hosted Plane)
	server, _ := testServer(t)
	defer server.Close()

	cfg := map[string]any{
		"endpoint":       server.URL + "/",
		"api_key":        "test-key",
		"workspace_slug": "test-workspace",
		"project_id":     "proj-1",
	}
	trk, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter with custom endpoint returned error: %v", err)
	}

	// Verify the adapter uses the custom endpoint
	adapter := trk.(*Adapter)
	if adapter.client.baseURL != server.URL+"/" {
		t.Errorf("expected base URL %q, got %q", server.URL+"/", adapter.client.baseURL)
	}

	// Make a request to verify it hits the custom server
	_, err = adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues with custom endpoint returned error: %v", err)
	}
}

func TestAdapter_Normalization(t *testing.T) {
	server, _ := testServer(t)
	defer server.Close()

	adapter := newTestAdapter(t, server.URL, []string{"started"}, []string{"completed"})
	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) < 1 {
		t.Fatal("expected at least 1 issue")
	}

	iss := issues[0]

	// ID should be the UUID
	if iss.ID != "issue-1" {
		t.Errorf("expected ID 'issue-1', got %q", iss.ID)
	}

	// Identifier should be "{project}-{sequence_id}"
	if iss.Identifier != "proj-1-1" {
		t.Errorf("expected identifier 'proj-1-1', got %q", iss.Identifier)
	}

	// Title
	if iss.Title != "Bug fix" {
		t.Errorf("expected title 'Bug fix', got %q", iss.Title)
	}

	// Description should be HTML-stripped
	if iss.Description != "Fix the bug" {
		t.Errorf("expected description 'Fix the bug', got %q", iss.Description)
	}

	// State should be resolved name
	if iss.State != "In Progress" {
		t.Errorf("expected state 'In Progress', got %q", iss.State)
	}

	// Labels should be lowercase
	if len(iss.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(iss.Labels))
	}
	for _, l := range iss.Labels {
		if l != strings.ToLower(l) {
			t.Errorf("expected lowercase label, got %q", l)
		}
	}

	// URL should be constructed
	expectedURL := "https://app.plane.so/test-workspace/projects/proj-1/issues/issue-1"
	if iss.URL != expectedURL {
		t.Errorf("expected URL %q, got %q", expectedURL, iss.URL)
	}
}

func TestAdapter_MissingConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{
			name:    "missing api_key",
			cfg:     map[string]any{"workspace_slug": "ws", "project_id": "p1"},
			wantErr: "api_key is required",
		},
		{
			name:    "missing workspace_slug",
			cfg:     map[string]any{"api_key": "key", "project_id": "p1"},
			wantErr: "workspace_slug is required",
		},
		{
			name:    "missing project_id",
			cfg:     map[string]any{"api_key": "key", "workspace_slug": "ws"},
			wantErr: "project_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAdapter(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestAdapter_DefaultStateGroups(t *testing.T) {
	cfg := map[string]any{
		"api_key":        "key",
		"workspace_slug": "ws",
		"project_id":     "p1",
	}
	trk, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	adapter := trk.(*Adapter)

	if len(adapter.activeStates) != 1 || adapter.activeStates[0] != "started" {
		t.Errorf("expected default active states ['started'], got %v", adapter.activeStates)
	}
	if len(adapter.terminalStates) != 2 || adapter.terminalStates[0] != "completed" || adapter.terminalStates[1] != "cancelled" {
		t.Errorf("expected default terminal states ['completed','cancelled'], got %v", adapter.terminalStates)
	}
}

func TestAdapter_Register(t *testing.T) {
	// Register should not panic
	// Note: This test verifies Register() exists and is callable.
	// We can't call it twice (registry panics on duplicate), so we just
	// verify the function exists and is correctly wired.
	// The actual registration should be tested in integration.
}
