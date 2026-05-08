package plane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "state-1", "name": "Todo", "group": "backlog"},
				{"id": "state-2", "name": "In Progress", "group": "started"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	result, err := client.Get(context.Background(), "test/path")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	results, ok := result["results"].([]any)
	if !ok {
		t.Fatalf("expected results to be a slice, got %T", result["results"])
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestClient_Post(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":           "comment-1",
			"comment_html": receivedBody["comment_html"],
		})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	result, err := client.Post(context.Background(), "test/path", map[string]string{"comment_html": "<p>Hello</p>"})
	if err != nil {
		t.Fatalf("Post returned error: %v", err)
	}
	if receivedBody["comment_html"] != "<p>Hello</p>" {
		t.Errorf("expected comment_html to be sent, got %q", receivedBody["comment_html"])
	}
	if result["id"] != "comment-1" {
		t.Errorf("expected id comment-1, got %v", result["id"])
	}
}

func TestClient_FetchStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedPath := "/workspaces/my-workspace/projects/proj-1/states/"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": "st-1", "name": "Backlog", "group": "backlog"},
				{"id": "st-2", "name": "In Progress", "group": "started"},
				{"id": "st-3", "name": "Done", "group": "completed"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	states, err := client.FetchStates(context.Background())
	if err != nil {
		t.Fatalf("FetchStates returned error: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("expected 3 states, got %d", len(states))
	}
	if states[0].Group != "backlog" {
		t.Errorf("expected group 'backlog', got %q", states[0].Group)
	}
	if states[1].Name != "In Progress" {
		t.Errorf("expected name 'In Progress', got %q", states[1].Name)
	}
}

func TestClient_FetchIssues_Pagination(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		offset := r.URL.Query().Get("offset")
		limit := r.URL.Query().Get("limit")

		if limit != "50" {
			t.Errorf("expected limit=50, got %s", limit)
		}

		var results []map[string]any
		if offset == "0" {
			// First page: 50 items
			for i := range 50 {
				results = append(results, map[string]any{
					"id":          fmt.Sprintf("issue-%d", i),
					"sequence_id": float64(i + 1),
					"name":        fmt.Sprintf("Issue %d", i),
					"state":       "st-2",
					"priority":    float64(2),
					"labels":      []any{},
				})
			}
		} else if offset == "50" {
			// Second page: 10 items (less than limit, so pagination stops)
			for i := 50; i < 60; i++ {
				results = append(results, map[string]any{
					"id":          fmt.Sprintf("issue-%d", i),
					"sequence_id": float64(i + 1),
					"name":        fmt.Sprintf("Issue %d", i),
					"state":       "st-2",
					"priority":    float64(2),
					"labels":      []any{},
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	issues, err := client.FetchIssues(context.Background(), nil)
	if err != nil {
		t.Fatalf("FetchIssues returned error: %v", err)
	}
	if len(issues) != 60 {
		t.Errorf("expected 60 issues across 2 pages, got %d", len(issues))
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

func TestClient_NetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond) // Delay longer than context timeout
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	// Use a very short timeout for the test
	client.http = &http.Client{Timeout: 10 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx, "test/path")
	if err == nil {
		t.Fatal("expected error for timeout, got nil")
	}
}

func TestClient_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail": "Invalid token"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "bad-key", "my-workspace", "proj-1")
	_, err := client.Get(context.Background(), "test/path")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to mention 401, got %q", err.Error())
	}
}

func TestClient_Patch(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "issue-1",
			"state": receivedBody["state"],
		})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	result, err := client.Patch(context.Background(), "test/path", map[string]string{"state": "st-3"})
	if err != nil {
		t.Fatalf("Patch returned error: %v", err)
	}
	if receivedBody["state"] != "st-3" {
		t.Errorf("expected state st-3 to be sent, got %q", receivedBody["state"])
	}
	if result["state"] != "st-3" {
		t.Errorf("expected state st-3 in response, got %v", result["state"])
	}
}

func TestClient_CreateComment(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		expectedPath := "/workspaces/my-workspace/projects/proj-1/issues/issue-1/comments/"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "comment-1"})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	err := client.CreateComment(context.Background(), "issue-1", "<p>Done!</p>")
	if err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}
	if receivedBody["comment_html"] != "<p>Done!</p>" {
		t.Errorf("expected comment_html '<p>Done!</p>', got %q", receivedBody["comment_html"])
	}
}

func TestClient_UpdateIssueState(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		expectedPath := "/workspaces/my-workspace/projects/proj-1/issues/issue-1/"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "issue-1", "state": receivedBody["state"]})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "test-key", "my-workspace", "proj-1")
	err := client.UpdateIssueState(context.Background(), "issue-1", "st-done")
	if err != nil {
		t.Fatalf("UpdateIssueState returned error: %v", err)
	}
	if receivedBody["state"] != "st-done" {
		t.Errorf("expected state 'st-done', got %q", receivedBody["state"])
	}
}
