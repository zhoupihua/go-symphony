package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zhoupihua/go-symphony/internal/tracker"
)

func makeIssue(id, identifier, title, state string) tracker.Issue {
	p := 1
	return tracker.Issue{
		ID:          id,
		Identifier:  identifier,
		Title:       title,
		Description: "desc",
		State:       state,
		Priority:    &p,
		Labels:      []string{"bug"},
		URL:         "https://example.com/" + id,
		BlockedBy:   nil,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func TestFetchCandidateIssues(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "open"),
		makeIssue("2", "ENG-2", "Second", "closed"),
	}
	m := NewMemoryAdapter(issues)

	got, err := m.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	// Verify it returns a copy, not a reference to the internal slice.
	got[0].State = "mutated"
	original, _ := m.FetchCandidateIssues(context.Background())
	if original[0].State == "mutated" {
		t.Error("FetchCandidateIssues should return a copy, not the internal slice")
	}
}

func TestFetchIssuesByStates(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "open"),
		makeIssue("2", "ENG-2", "Second", "in_progress"),
		makeIssue("3", "ENG-3", "Third", "closed"),
	}
	m := NewMemoryAdapter(issues)

	got, err := m.FetchIssuesByStates(context.Background(), []string{"open", "closed"})
	if err != nil {
		t.Fatalf("FetchIssuesByStates returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	for _, iss := range got {
		if iss.State != "open" && iss.State != "closed" {
			t.Errorf("unexpected state %q", iss.State)
		}
	}
}

func TestFetchIssuesByStates_CaseInsensitive(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "Open"),
		makeIssue("2", "ENG-2", "Second", "IN_PROGRESS"),
	}
	m := NewMemoryAdapter(issues)

	got, err := m.FetchIssuesByStates(context.Background(), []string{"open", "in_progress"})
	if err != nil {
		t.Fatalf("FetchIssuesByStates returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("case-insensitive: expected 2 issues, got %d", len(got))
	}
}

func TestFetchIssueStatesByIDs(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "open"),
		makeIssue("2", "ENG-2", "Second", "closed"),
		makeIssue("3", "ENG-3", "Third", "open"),
	}
	m := NewMemoryAdapter(issues)

	got, err := m.FetchIssueStatesByIDs(context.Background(), []string{"1", "3"})
	if err != nil {
		t.Fatalf("FetchIssueStatesByIDs returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	for _, iss := range got {
		if iss.ID != "1" && iss.ID != "3" {
			t.Errorf("unexpected ID %q", iss.ID)
		}
	}
}

func TestCreateComment(t *testing.T) {
	m := NewMemoryAdapter(nil)

	if err := m.CreateComment(context.Background(), "1", "first comment"); err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}
	if err := m.CreateComment(context.Background(), "1", "second comment"); err != nil {
		t.Fatalf("CreateComment returned error: %v", err)
	}

	comments := m.Comments("1")
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0] != "first comment" {
		t.Errorf("expected 'first comment', got %q", comments[0])
	}
	if comments[1] != "second comment" {
		t.Errorf("expected 'second comment', got %q", comments[1])
	}
}

func TestUpdateIssueState(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "open"),
	}
	m := NewMemoryAdapter(issues)

	if err := m.UpdateIssueState(context.Background(), "1", "in_progress"); err != nil {
		t.Fatalf("UpdateIssueState returned error: %v", err)
	}

	got, _ := m.FetchCandidateIssues(context.Background())
	if got[0].State != "in_progress" {
		t.Errorf("expected state 'in_progress', got %q", got[0].State)
	}
}

func TestUpdateIssueState_NotFound(t *testing.T) {
	m := NewMemoryAdapter(nil)

	err := m.UpdateIssueState(context.Background(), "nonexistent", "closed")
	if err == nil {
		t.Fatal("expected error for non-existent issue, got nil")
	}
}

func TestEmptyIssues_ReturnsEmptySliceNotNil(t *testing.T) {
	m := NewMemoryAdapter(nil)

	got, err := m.FetchCandidateIssues(context.Background())
	if err != nil {
		t.Fatalf("FetchCandidateIssues returned error: %v", err)
	}
	if got == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 issues, got %d", len(got))
	}

	gotStates, err := m.FetchIssuesByStates(context.Background(), []string{"open"})
	if err != nil {
		t.Fatalf("FetchIssuesByStates returned error: %v", err)
	}
	if gotStates == nil {
		t.Error("FetchIssuesByStates: expected empty slice, got nil")
	}

	gotIDs, err := m.FetchIssueStatesByIDs(context.Background(), []string{"1"})
	if err != nil {
		t.Fatalf("FetchIssueStatesByIDs returned error: %v", err)
	}
	if gotIDs == nil {
		t.Error("FetchIssueStatesByIDs: expected empty slice, got nil")
	}
}

func TestConcurrentAccess(t *testing.T) {
	issues := []tracker.Issue{
		makeIssue("1", "ENG-1", "First", "open"),
		makeIssue("2", "ENG-2", "Second", "closed"),
	}
	m := NewMemoryAdapter(issues)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(4)

		go func() {
			defer wg.Done()
			m.FetchCandidateIssues(context.Background())
		}()

		go func() {
			defer wg.Done()
			m.FetchIssuesByStates(context.Background(), []string{"open"})
		}()

		go func() {
			defer wg.Done()
			m.UpdateIssueState(context.Background(), "1", "in_progress")
		}()

		go func() {
			defer wg.Done()
			m.CreateComment(context.Background(), "1", "comment")
		}()
	}
	wg.Wait()
}
