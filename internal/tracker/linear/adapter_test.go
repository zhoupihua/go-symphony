package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// newTestAdapter creates an Adapter backed by a test HTTP server.
func newTestAdapter(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg := map[string]any{
		"endpoint":        srv.URL,
		"api_key":         "test-key",
		"project_slug":    "test-project",
		"active_states":   []string{"Backlog", "In Progress"},
		"terminal_states": []string{"Done", "Cancelled"},
	}

	trk, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	return trk.(*Adapter)
}

func TestFetchCandidateIssues(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		stateNames, _ := req.Variables["stateNames"].([]any)
		var filtered []map[string]any

		allIssues := []map[string]any{
			issueNode("id-1", "ENG-1", "Bug fix", "Backlog", intPtr(1), nil),
			issueNode("id-2", "ENG-2", "Feature", "In Progress", intPtr(2), nil),
			issueNode("id-3", "ENG-3", "Done task", "Done", nil, nil),
		}

		stateSet := map[string]bool{}
		for _, s := range stateNames {
			if sv, ok := s.(string); ok {
				stateSet[sv] = true
			}
		}

		for _, iss := range allIssues {
			st := iss["state"].(map[string]any)["name"].(string)
			if stateSet[st] {
				filtered = append(filtered, iss)
			}
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    filtered,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 candidate issues, got %d", len(issues))
	}
	for _, iss := range issues {
		if iss.State == "Done" {
			t.Errorf("should not include Done state issue, got %q", iss.Identifier)
		}
	}
}

func TestFetchIssuesByStates(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		allIssues := []map[string]any{
			issueNode("id-1", "ENG-1", "Bug fix", "Backlog", intPtr(1), nil),
			issueNode("id-2", "ENG-2", "Feature", "In Progress", intPtr(2), nil),
			issueNode("id-3", "ENG-3", "Done task", "Done", nil, nil),
		}

		stateNames, _ := req.Variables["stateNames"].([]any)
		stateSet := map[string]bool{}
		for _, s := range stateNames {
			if sv, ok := s.(string); ok {
				stateSet[sv] = true
			}
		}

		var filtered []map[string]any
		for _, iss := range allIssues {
			st := iss["state"].(map[string]any)["name"].(string)
			if stateSet[st] {
				filtered = append(filtered, iss)
			}
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    filtered,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchIssuesByStates(context.Background(), []string{"Done"})
	if err != nil {
		t.Fatalf("FetchIssuesByStates returned error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue with Done state, got %d", len(issues))
	}
	if issues[0].Identifier != "ENG-3" {
		t.Errorf("expected ENG-3, got %s", issues[0].Identifier)
	}
}

func TestFetchIssuesByStates_Empty(t *testing.T) {
	adapter := newTestAdapter(t, nil)
	issues, err := adapter.FetchIssuesByStates(context.Background(), []string{})
	if err != nil {
		t.Fatalf("FetchIssuesByStates with empty states returned error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for empty states, got %d", len(issues))
	}
}

func TestFetchIssueStatesByIDs(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		ids, _ := req.Variables["ids"].([]any)
		var nodes []map[string]any
		for _, idRaw := range ids {
			id, _ := idRaw.(string)
			nodes = append(nodes, issueNode(id, "ENG-"+id, "Issue "+id, "In Progress", intPtr(1), nil))
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": nodes,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchIssueStatesByIDs(context.Background(), []string{"1", "3"})
	if err != nil {
		t.Fatalf("FetchIssueStatesByIDs returned error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	for _, iss := range issues {
		if iss.ID != "1" && iss.ID != "3" {
			t.Errorf("unexpected ID %q", iss.ID)
		}
	}
}

func TestFetchIssueStatesByIDs_Empty(t *testing.T) {
	adapter := newTestAdapter(t, nil)
	issues, err := adapter.FetchIssueStatesByIDs(context.Background(), []string{})
	if err != nil {
		t.Fatalf("FetchIssueStatesByIDs with empty ids returned error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues for empty ids, got %d", len(issues))
	}
}

func TestCreateComment(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"data": map[string]any{
				"commentCreate": map[string]any{"success": true},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	err := adapter.CreateComment(context.Background(), "issue-1", "hello world")
	if err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}
}

func TestCreateComment_Failure(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"commentCreate": map[string]any{"success": false},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	err := adapter.CreateComment(context.Background(), "issue-1", "hello world")
	if err == nil {
		t.Fatal("expected error for failed comment creation")
	}
}

func TestUpdateIssueState(t *testing.T) {
	var requestCount int
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// First request is the state lookup, second is the update
		if requestCount == 1 {
			// resolveStateId query
			resp := map[string]any{
				"data": map[string]any{
					"issue": map[string]any{
						"team": map[string]any{
							"states": map[string]any{
								"nodes": []any{
									map[string]any{"id": "state-in-progress"},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		// issueUpdate mutation
		resp := map[string]any{
			"data": map[string]any{
				"issueUpdate": map[string]any{"success": true},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	err := adapter.UpdateIssueState(context.Background(), "issue-1", "In Progress")
	if err != nil {
		t.Fatalf("UpdateIssueState returned error: %v", err)
	}
}

func TestUpdateIssueState_Failure(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		// State lookup returns valid result
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Check if this is the state lookup or the update
		if vars, ok := req.Variables["stateName"]; ok && vars != nil {
			// State lookup query
			resp := map[string]any{
				"data": map[string]any{
					"issue": map[string]any{
						"team": map[string]any{
							"states": map[string]any{
								"nodes": []any{
									map[string]any{"id": "state-done"},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Update mutation - return failure
		resp := map[string]any{
			"data": map[string]any{
				"issueUpdate": map[string]any{"success": false},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	err := adapter.UpdateIssueState(context.Background(), "issue-1", "Done")
	if err == nil {
		t.Fatal("expected error for failed issue update")
	}
}

func TestLabels_NormalizedToLowercase(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		nodes := []map[string]any{
			{
				"id":          "id-1",
				"identifier":  "ENG-1",
				"title":       "Labeled Issue",
				"description": "desc",
				"priority":    nil,
				"state":       map[string]any{"name": "Backlog"},
				"url":         "https://linear.app/issue/ENG-1",
				"labels": map[string]any{"nodes": []any{
					map[string]any{"name": "Bug"},
					map[string]any{"name": "HIGH_PRIORITY"},
					map[string]any{"name": "Feature Request"},
				}},
				"inverseRelations": map[string]any{"nodes": []any{}},
				"createdAt":        "2024-01-15T10:30:00Z",
				"updatedAt":        "2024-01-16T14:20:00Z",
			},
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	expectedLabels := []string{"bug", "high_priority", "feature request"}
	if len(issues[0].Labels) != len(expectedLabels) {
		t.Fatalf("expected %d labels, got %d", len(expectedLabels), len(issues[0].Labels))
	}
	for i, label := range issues[0].Labels {
		if label != expectedLabels[i] {
			t.Errorf("label[%d]: expected %q, got %q", i, expectedLabels[i], label)
		}
	}
}

func TestPriority_NilHandling(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		nodes := []map[string]any{
			issueNode("id-1", "ENG-1", "No Priority", "Backlog", nil, nil),
			issueNode("id-2", "ENG-2", "With Priority", "Backlog", intPtr(3), nil),
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}

	// First issue should have nil priority
	if issues[0].Priority != nil {
		t.Errorf("expected nil priority for first issue, got %v", issues[0].Priority)
	}

	// Second issue should have priority 3
	if issues[1].Priority == nil {
		t.Fatal("expected non-nil priority for second issue")
	}
	if *issues[1].Priority != 3 {
		t.Errorf("expected priority 3, got %d", *issues[1].Priority)
	}
}

func TestBlockedBy_FromInverseRelations(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		nodes := []map[string]any{
			issueNode("id-1", "ENG-1", "Blocked Issue", "Backlog", nil, []string{"ENG-5", "ENG-6"}),
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	if len(issues[0].BlockedBy) != 2 {
		t.Fatalf("expected 2 blocked_by entries, got %d", len(issues[0].BlockedBy))
	}
	if issues[0].BlockedBy[0].Identifier != "ENG-5" {
		t.Errorf("expected blocked_by[0] 'ENG-5', got %q", issues[0].BlockedBy[0].Identifier)
	}
	if issues[0].BlockedBy[1].Identifier != "ENG-6" {
		t.Errorf("expected blocked_by[1] 'ENG-6', got %q", issues[0].BlockedBy[1].Identifier)
	}
}

func TestNewAdapter_MissingAPIKey(t *testing.T) {
	cfg := map[string]any{
		"project_slug": "test-project",
	}
	_, err := NewAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
}

func TestNewAdapter_MissingProjectSlug(t *testing.T) {
	cfg := map[string]any{
		"api_key": "test-key",
	}
	_, err := NewAdapter(cfg)
	if err == nil {
		t.Fatal("expected error for missing project_slug")
	}
}

func TestNewAdapter_DefaultEndpoint(t *testing.T) {
	cfg := map[string]any{
		"api_key":      "test-key",
		"project_slug": "test-project",
	}
	trk, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter returned error: %v", err)
	}
	adapter := trk.(*Adapter)
	if adapter.client.endpoint != defaultEndpoint {
		t.Errorf("expected default endpoint %q, got %q", defaultEndpoint, adapter.client.endpoint)
	}
}

func TestRegister(t *testing.T) {
	// Register should not panic
	Register()

	// Verify we can create a tracker via the registry
	cfg := map[string]any{
		"api_key":         "test-key",
		"project_slug":    "test-project",
		"active_states":   []string{"Backlog"},
		"terminal_states": []string{"Done"},
	}
	trk, err := tracker.NewTracker("linear", cfg)
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	if _, ok := trk.(*Adapter); !ok {
		t.Error("expected *linear.Adapter from registry")
	}
}

func TestTimestamps(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		nodes := []map[string]any{
			{
				"id":          "id-1",
				"identifier":  "ENG-1",
				"title":       "Timestamped Issue",
				"description": "desc",
				"priority":    nil,
				"state":       map[string]any{"name": "Backlog"},
				"url":         "https://linear.app/issue/ENG-1",
				"labels":      map[string]any{"nodes": []any{}},
				"inverseRelations": map[string]any{"nodes": []any{}},
				"createdAt":        "2024-03-15T08:30:00Z",
				"updatedAt":        "2024-03-16T16:45:00Z",
			},
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	issues, err := adapter.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	iss := issues[0]
	if iss.CreatedAt.Year() != 2024 || iss.CreatedAt.Month() != 3 || iss.CreatedAt.Day() != 15 {
		t.Errorf("unexpected CreatedAt: %v", iss.CreatedAt)
	}
	if iss.UpdatedAt.Year() != 2024 || iss.UpdatedAt.Month() != 3 || iss.UpdatedAt.Day() != 16 {
		t.Errorf("unexpected UpdatedAt: %v", iss.UpdatedAt)
	}
}

// intPtr returns a pointer to the given int.
func intPtr(v int) *int {
	return &v
}
