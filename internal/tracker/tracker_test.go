package tracker

import (
	"context"
	"testing"
)

// stubTracker is a minimal Tracker implementation for registry tests.
type stubTracker struct{}

func (stubTracker) FetchCandidateIssues(_ context.Context) ([]Issue, error)        { return nil, nil }
func (stubTracker) FetchIssuesByStates(_ context.Context, _ []string) ([]Issue, error) {
	return nil, nil
}
func (stubTracker) FetchIssueStatesByIDs(_ context.Context, _ []string) ([]Issue, error) {
	return nil, nil
}
func (stubTracker) CreateComment(_ context.Context, _, _ string) error  { return nil }
func (stubTracker) UpdateIssueState(_ context.Context, _, _ string) error { return nil }

func TestRegisterAndNewTracker(t *testing.T) {
	// Use a separate registry map to avoid cross-test pollution.
	orig := trackerRegistry
	trackerRegistry = map[string]TrackerFactory{}
	defer func() { trackerRegistry = orig }()

	RegisterTracker("test", func(_ map[string]any) (Tracker, error) {
		return stubTracker{}, nil
	})

	tr, err := NewTracker("test", nil)
	if err != nil {
		t.Fatalf("NewTracker returned error: %v", err)
	}
	if _, ok := tr.(stubTracker); !ok {
		t.Fatalf("expected stubTracker, got %T", tr)
	}
}

func TestNewTrackerUnknownKind(t *testing.T) {
	orig := trackerRegistry
	trackerRegistry = map[string]TrackerFactory{}
	defer func() { trackerRegistry = orig }()

	_, err := NewTracker("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

func TestRegisterTrackerDuplicatePanics(t *testing.T) {
	orig := trackerRegistry
	trackerRegistry = map[string]TrackerFactory{}
	defer func() { trackerRegistry = orig }()

	factory := func(_ map[string]any) (Tracker, error) { return stubTracker{}, nil }
	RegisterTracker("dup", factory)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration, got none")
		}
	}()
	RegisterTracker("dup", factory)
}
