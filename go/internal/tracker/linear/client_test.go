package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuery_ValidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers
		if r.Header.Get("Authorization") != "lin_api_testkey" {
			t.Errorf("expected Authorization header 'lin_api_testkey', got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got %q", r.Header.Get("Content-Type"))
		}

		// Decode request body
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Query == "" {
			t.Error("query should not be empty")
		}

		// Return successful response
		resp := graphqlResponse{
			Data: map[string]any{
				"viewer": map[string]any{"id": "user-1"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "lin_api_testkey", "test-project")
	data, err := client.Query(context.Background(), "{ viewer { id } }", nil)
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
	viewer, ok := data["viewer"].(map[string]any)
	if !ok {
		t.Fatal("expected viewer in data")
	}
	if viewer["id"] != "user-1" {
		t.Errorf("expected viewer.id 'user-1', got %v", viewer["id"])
	}
}

func TestQuery_GraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse{
			Errors: []graphqlError{
				{Message: "something went wrong"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "test-project")
	_, err := client.Query(context.Background(), "{ bad }", nil)
	if err == nil {
		t.Fatal("expected error for GraphQL errors response")
	}
	if err.Error() != "graphql error: something went wrong" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestQuery_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "test-project")
	_, err := client.Query(context.Background(), "{ viewer { id } }", nil)
	if err == nil {
		t.Fatal("expected error for non-200 HTTP status")
	}
}

func TestQuery_NetworkTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "test-project")
	client.http.Timeout = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := client.Query(ctx, "{ viewer { id } }", nil)
	if err == nil {
		t.Fatal("expected error for network timeout")
	}
}

func TestFetchIssues_Pagination(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		call := callCount.Add(1)
		after, _ := req.Variables["after"].(string)

		var nodes []map[string]any
		var hasNext bool
		var endCursor string

		if after == "" {
			// Page 1: two issues, has next page
			nodes = []map[string]any{
				issueNode("id-1", "ENG-1", "First Issue", "Backlog", intPtr(1), nil),
				issueNode("id-2", "ENG-2", "Second Issue", "In Progress", intPtr(2), nil),
			}
			hasNext = true
			endCursor = "cursor-page2"
		} else if after == "cursor-page2" {
			// Page 2: one issue, no more pages
			nodes = []map[string]any{
				issueNode("id-3", "ENG-3", "Third Issue", "Done", nil, nil),
			}
			hasNext = false
			endCursor = ""
		} else {
			t.Errorf("unexpected after cursor: %q (call %d)", after, call)
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": nodes,
					"pageInfo": map[string]any{
						"hasNextPage": hasNext,
						"endCursor":   endCursor,
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "test-project")
	filter := map[string]any{"stateNames": []string{"Backlog", "In Progress"}}
	issues, err := client.FetchIssues(context.Background(), filter)
	if err != nil {
		t.Fatalf("FetchIssues returned error: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}
	if issues[0].Identifier != "ENG-1" {
		t.Errorf("expected first issue ENG-1, got %s", issues[0].Identifier)
	}
	if issues[2].Identifier != "ENG-3" {
		t.Errorf("expected third issue ENG-3, got %s", issues[2].Identifier)
	}
}

func TestFetchIssuesByIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		ids, _ := req.Variables["ids"].([]any)
		var nodes []map[string]any
		for _, idRaw := range ids {
			id, _ := idRaw.(string)
			nodes = append(nodes, issueNode(id, "ENG-"+id[len(id)-1:], "Issue "+id, "In Progress", intPtr(1), nil))
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
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "test-project")
	issues, err := client.FetchIssuesByIDs(context.Background(), []string{"1", "3"})
	if err != nil {
		t.Fatalf("FetchIssuesByIDs returned error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
}

// issueNode builds a raw issue map for test GraphQL responses.
func issueNode(id, identifier, title, state string, priority *int, blockedBy []string) map[string]any {
	node := map[string]any{
		"id":          id,
		"identifier":  identifier,
		"title":       title,
		"description":  fmt.Sprintf("Description for %s", title),
		"priority":    priority,
		"state":       map[string]any{"name": state},
		"url":         fmt.Sprintf("https://linear.app/issue/%s", identifier),
		"labels":      map[string]any{"nodes": []any{}},
		"inverseRelations": map[string]any{"nodes": []any{}},
		"createdAt":   "2024-01-15T10:30:00Z",
		"updatedAt":   "2024-01-16T14:20:00Z",
	}

	if len(blockedBy) > 0 {
		var relNodes []any
		for _, b := range blockedBy {
			relNodes = append(relNodes, map[string]any{
				"type": "blocks",
				"issue": map[string]any{
					"id":         b + "-id",
					"identifier": b,
					"state":      map[string]any{"name": "In Progress"},
				},
			})
		}
		node["inverseRelations"] = map[string]any{"nodes": relNodes}
	}

	return node
}
