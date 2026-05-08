package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ainative/go-symphony/internal/tracker"
)

// Compile-time interface check.
var _ tracker.Tracker = (*MemoryAdapter)(nil)

// MemoryAdapter is an in-memory tracker adapter for testing and development.
type MemoryAdapter struct {
	mu       sync.RWMutex
	issues   []tracker.Issue
	comments map[string][]string
}

// NewMemoryAdapter creates an in-memory tracker with the given issues.
func NewMemoryAdapter(issues []tracker.Issue) *MemoryAdapter {
	return &MemoryAdapter{
		issues:   issues,
		comments: make(map[string][]string),
	}
}

// FetchCandidateIssues returns all issues (in a real adapter, this would filter
// by active states, but for the memory adapter we return everything).
func (m *MemoryAdapter) FetchCandidateIssues(ctx context.Context) ([]tracker.Issue, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]tracker.Issue, len(m.issues))
	copy(result, m.issues)
	return result, nil
}

// FetchIssuesByStates returns issues whose State is in the given list.
func (m *MemoryAdapter) FetchIssuesByStates(ctx context.Context, states []string) ([]tracker.Issue, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stateSet := make(map[string]bool, len(states))
	for _, s := range states {
		stateSet[strings.ToLower(s)] = true
	}
	var result []tracker.Issue
	for _, issue := range m.issues {
		if stateSet[strings.ToLower(issue.State)] {
			result = append(result, issue)
		}
	}
	if result == nil {
		result = []tracker.Issue{}
	}
	return result, nil
}

// FetchIssueStatesByIDs returns issues whose ID is in the given list.
func (m *MemoryAdapter) FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]tracker.Issue, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var result []tracker.Issue
	for _, issue := range m.issues {
		if idSet[issue.ID] {
			result = append(result, issue)
		}
	}
	if result == nil {
		result = []tracker.Issue{}
	}
	return result, nil
}

// CreateComment stores a comment for the given issue.
func (m *MemoryAdapter) CreateComment(ctx context.Context, issueID, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments[issueID] = append(m.comments[issueID], body)
	return nil
}

// UpdateIssueState updates the state of the issue with the given ID.
func (m *MemoryAdapter) UpdateIssueState(ctx context.Context, issueID, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.issues {
		if m.issues[i].ID == issueID {
			m.issues[i].State = state
			return nil
		}
	}
	return fmt.Errorf("issue not found: %s", issueID)
}

// SetIssues replaces the stored issues (useful for test setup).
func (m *MemoryAdapter) SetIssues(issues []tracker.Issue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issues = issues
}

// Comments returns all comments for a given issue (test helper).
func (m *MemoryAdapter) Comments(issueID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.comments[issueID]
}
