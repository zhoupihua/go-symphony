package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/tracker"
)

// stubTrackerForOrch implements tracker.Tracker.
type stubTrackerForOrch struct {
	mu        sync.Mutex
	candidates []tracker.Issue
	byStates  []tracker.Issue
	byIDs     []tracker.Issue
	fetchErr  error
}

func (t *stubTrackerForOrch) FetchCandidateIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]tracker.Issue{}, t.candidates...), t.fetchErr
}
func (t *stubTrackerForOrch) FetchIssuesByStates(_ context.Context, _ []string) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]tracker.Issue{}, t.byStates...), t.fetchErr
}
func (t *stubTrackerForOrch) FetchIssueStatesByIDs(_ context.Context, _ []string) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]tracker.Issue{}, t.byIDs...), t.fetchErr
}
func (t *stubTrackerForOrch) CreateComment(_ context.Context, _, _ string) error { return nil }
func (t *stubTrackerForOrch) UpdateIssueState(_ context.Context, _, _ string) error {
	return nil
}

// stubAgentForOrch implements agent.Agent.
type stubAgentForOrch struct {
	session agent.Session
}

func (a *stubAgentForOrch) StartSession(_ context.Context, _ agent.SessionOptions) (agent.Session, error) {
	if a.session != nil {
		return a.session, nil
	}
	return &stubSessionOrch{}, nil
}

type stubSessionOrch struct {
	closed bool
}

func (s *stubSessionOrch) RunTurn(_ context.Context, _ string, _ agent.TurnOptions) (agent.TurnResult, error) {
	return agent.TurnResult{Completed: true, Output: "done"}, nil
}

func (s *stubSessionOrch) Close() error {
	s.closed = true
	return nil
}

func intPtr(v int) *int { return &v } //nolint:unused // used by sort tests

func testOrchConfig() config.Schema {
	return config.Schema{
		Workspace: config.WorkspaceConfig{
			Root: "/tmp/test-workspaces",
		},
		Agent: config.AgentConfig{
			Kind:          "codex",
			MaxConcurrent: 5,
			MaxTurns:      3,
			Codex:         config.CodexConfig{Command: "codex app-server"},
		},
		Tracker: config.TrackerConfig{
			Kind:          "linear",
			APIKey:        "test-key",
			ActiveStates:  []string{"in_progress", "todo"},
			TerminalStates: []string{"done", "cancelled"},
			Linear:        config.LinearConfig{ProjectSlug: "TEST"},
		},
		Polling: config.PollingConfig{
			IntervalMS: 100,
		},
	}
}

func TestSortIssuesByPriority(t *testing.T) {
	issues := []tracker.Issue{
		{ID: "3", Identifier: "C", Priority: intPtr(3)},
		{ID: "1", Identifier: "A", Priority: intPtr(1)},
		{ID: "2", Identifier: "B", Priority: intPtr(2)},
		{ID: "0", Identifier: "D"}, // nil priority
	}

	sortIssues(issues)

	if issues[0].ID != "1" {
		t.Errorf("first issue ID = %q, want %q", issues[0].ID, "1")
	}
	if issues[3].ID != "0" {
		t.Errorf("last issue ID = %q, want %q (nil priority last)", issues[3].ID, "0")
	}
}

func TestSortIssuesByCreatedDate(t *testing.T) {
	now := time.Now()
	issues := []tracker.Issue{
		{ID: "2", Identifier: "B", CreatedAt: now.Add(2 * time.Hour)},
		{ID: "1", Identifier: "A", CreatedAt: now.Add(1 * time.Hour)},
	}

	sortIssues(issues)

	if issues[0].ID != "1" {
		t.Errorf("first issue ID = %q, want %q (older first)", issues[0].ID, "1")
	}
}

func TestCanDispatchBasic(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	issue := tracker.Issue{ID: "1", State: "in_progress"}
	if !o.canDispatch(cfg, issue) {
		t.Error("should be able to dispatch fresh issue")
	}
}

func TestCanDispatchAlreadyRunning(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	issue := tracker.Issue{ID: "1", State: "in_progress"}
	o.State.SetRunning("1", &RunInfo{Issue: issue})

	if o.canDispatch(cfg, issue) {
		t.Error("should not dispatch already running issue")
	}
}

func TestCanDispatchAlreadyClaimed(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	issue := tracker.Issue{ID: "1", State: "in_progress"}
	o.State.Claim("1")

	if o.canDispatch(cfg, issue) {
		t.Error("should not dispatch already claimed issue")
	}
}

func TestCanDispatchConcurrencyLimit(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Agent.MaxConcurrent = 2
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	o.State.SetRunning("1", &RunInfo{})
	o.State.SetRunning("2", &RunInfo{})

	issue := tracker.Issue{ID: "3", State: "in_progress"}
	if o.canDispatch(cfg, issue) {
		t.Error("should not dispatch when at concurrency limit")
	}
}

func TestCanDispatchPerStateLimit(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Agent.MaxConcurrentByState = map[string]int{"in_progress": 1}
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	o.State.SetRunning("1", &RunInfo{Issue: tracker.Issue{State: "in_progress"}})

	issue := tracker.Issue{ID: "2", State: "in_progress"}
	if o.canDispatch(cfg, issue) {
		t.Error("should not dispatch when per-state limit reached")
	}
}

func TestCanDispatchInactiveState(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	issue := tracker.Issue{ID: "1", State: "done"}
	if o.canDispatch(cfg, issue) {
		t.Error("should not dispatch issue in terminal state")
	}
}

func TestIsActiveState(t *testing.T) {
	if !isActiveState("in_progress", []string{"in_progress", "todo"}) {
		t.Error("in_progress should be active")
	}
	if isActiveState("done", []string{"in_progress", "todo"}) {
		t.Error("done should not be active")
	}
	if !isActiveState("anything", nil) {
		t.Error("nil active states means everything is active")
	}
}

func TestIsTerminalState(t *testing.T) {
	if !isTerminalState("done", []string{"done", "cancelled"}) {
		t.Error("done should be terminal")
	}
	if isTerminalState("in_progress", []string{"done", "cancelled"}) {
		t.Error("in_progress should not be terminal")
	}
}

func TestStateRunning(t *testing.T) {
	s := NewState()

	s.SetRunning("1", &RunInfo{Issue: tracker.Issue{ID: "1"}})
	if !s.IsRunning("1") {
		t.Error("should be running")
	}
	if s.RunningCount() != 1 {
		t.Errorf("count = %d, want 1", s.RunningCount())
	}

	info, ok := s.RemoveRunning("1")
	if !ok || info.Issue.ID != "1" {
		t.Error("should remove and return running info")
	}
	if s.IsRunning("1") {
		t.Error("should no longer be running")
	}
}

func TestStateClaim(t *testing.T) {
	s := NewState()

	s.Claim("1")
	if !s.IsClaimed("1") {
		t.Error("should be claimed")
	}
	s.ReleaseClaim("1")
	if s.IsClaimed("1") {
		t.Error("should no longer be claimed")
	}
}

func TestStateRetry(t *testing.T) {
	s := NewState()

	s.SetRetry("1", &RetryEntry{Attempt: 2})
	pending := s.PendingRetries()
	if len(pending) != 1 || pending["1"].Attempt != 2 {
		t.Errorf("pending retries = %v, want 1 entry with attempt 2", pending)
	}

	entry, ok := s.RemoveRetry("1")
	if !ok || entry.Attempt != 2 {
		t.Error("should remove and return retry entry")
	}
}

func TestStateCompleted(t *testing.T) {
	s := NewState()

	s.MarkCompleted("1")
	if !s.IsCompleted("1") {
		t.Error("should be completed")
	}
}

func TestStateRunningByState(t *testing.T) {
	s := NewState()
	s.SetRunning("1", &RunInfo{Issue: tracker.Issue{ID: "1", State: "in_progress"}})
	s.SetRunning("2", &RunInfo{Issue: tracker.Issue{ID: "2", State: "in_progress"}})
	s.SetRunning("3", &RunInfo{Issue: tracker.Issue{ID: "3", State: "todo"}})

	counts := s.RunningByState()
	if counts["in_progress"] != 2 {
		t.Errorf("in_progress count = %d, want 2", counts["in_progress"])
	}
	if counts["todo"] != 1 {
		t.Errorf("todo count = %d, want 1", counts["todo"])
	}
}

func TestFailureRetryDelay(t *testing.T) {
	cfg := config.Schema{Agent: config.AgentConfig{MaxRetryBackoffMS: 300000}}

	d1 := failureRetryDelay(1, cfg)
	if d1 != 10*time.Second {
		t.Errorf("attempt 1 delay = %v, want 10s", d1)
	}

	d2 := failureRetryDelay(2, cfg)
	if d2 != 20*time.Second {
		t.Errorf("attempt 2 delay = %v, want 20s", d2)
	}

	d3 := failureRetryDelay(3, cfg)
	if d3 != 40*time.Second {
		t.Errorf("attempt 3 delay = %v, want 40s", d3)
	}
}

func TestPollIntervalDefault(t *testing.T) {
	cfg := config.Schema{}
	d := cfgPollInterval(cfg)
	if d != 30*time.Second {
		t.Errorf("default poll interval = %v, want 30s", d)
	}
}

func TestPollIntervalConfigured(t *testing.T) {
	cfg := config.Schema{Polling: config.PollingConfig{IntervalMS: 5000}}
	d := cfgPollInterval(cfg)
	if d != 5*time.Second {
		t.Errorf("configured poll interval = %v, want 5s", d)
	}
}

func TestDispatchIssuesFromTracker(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Workspace.Root = t.TempDir()

	tr := &stubTrackerForOrch{
		candidates: []tracker.Issue{
			{ID: "1", Identifier: "PROJ-1", State: "in_progress", Title: "Bug 1"},
		},
	}

	sess := &stubSessionOrch{}
	ag := &stubAgentForOrch{session: sess}

	o := New(tr, ag, nil, func() config.Schema { return cfg }, func() string { return "Fix: {{.Issue.Title}}" })

	// Run one tick manually.
	o.tick(context.Background())

	// Wait for dispatch goroutine to start.
	time.Sleep(200 * time.Millisecond)

	// The issue should be claimed and running.
	if !o.State.IsClaimed("1") {
		t.Error("issue should be claimed after dispatch")
	}
}

func TestReconcileStalled(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Agent.Codex.StallTimeoutMS = 100 // 100ms stall timeout

	tr := &stubTrackerForOrch{}
	o := New(tr, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	// Add a running entry that's been idle for too long.
	info := &RunInfo{
		Issue:        tracker.Issue{ID: "1", State: "in_progress"},
		StartedAt:    time.Now().Add(-1 * time.Second),
		LastActivity: time.Now().Add(-1 * time.Second),
	}
	o.State.SetRunning("1", info)

	o.reconcileRunning(context.Background(), cfg)

	if o.State.IsRunning("1") {
		t.Error("stalled issue should be removed from running")
	}
}

func TestValidateDispatchConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Schema
		wantErr bool
	}{
		{
			name:    "valid codex config",
			cfg:     testOrchConfig(),
			wantErr: false,
		},
		{
			name: "missing tracker kind",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{APIKey: "key"},
				Agent:   config.AgentConfig{Kind: "codex", Codex: config.CodexConfig{Command: "codex"}},
			},
			wantErr: true,
		},
		{
			name: "missing tracker api_key",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear"},
				Agent:   config.AgentConfig{Kind: "codex", Codex: config.CodexConfig{Command: "codex"}},
			},
			wantErr: true,
		},
		{
			name: "missing linear project_slug",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear", APIKey: "key"},
				Agent:   config.AgentConfig{Kind: "codex", Codex: config.CodexConfig{Command: "codex"}},
			},
			wantErr: true,
		},
		{
			name: "missing agent kind",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear", APIKey: "key"},
				Agent:   config.AgentConfig{Codex: config.CodexConfig{Command: "codex"}},
			},
			wantErr: true,
		},
		{
			name: "missing codex command",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear", APIKey: "key"},
				Agent:   config.AgentConfig{Kind: "codex"},
			},
			wantErr: true,
		},
		{
			name: "valid claude config",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear", APIKey: "key", Linear: config.LinearConfig{ProjectSlug: "P"}},
				Agent:   config.AgentConfig{Kind: "claude", Claude: config.ClaudeConfig{Command: "claude"}},
			},
			wantErr: false,
		},
		{
			name: "missing claude command",
			cfg: config.Schema{
				Tracker: config.TrackerConfig{Kind: "linear", APIKey: "key", Linear: config.LinearConfig{ProjectSlug: "P"}},
				Agent:   config.AgentConfig{Kind: "claude"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDispatchConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDispatchConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCanDispatch_CompletedNotBlocking(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	// Mark an issue as completed — it should still be dispatchable
	// because the completed set no longer gates dispatch.
	o.State.MarkCompleted("1")

	issue := tracker.Issue{ID: "1", State: "in_progress"}
	if !o.canDispatch(cfg, issue) {
		t.Error("completed set should not block dispatch")
	}
}

func TestReconcile_ActiveState_UpdatesSnapshot(t *testing.T) {
	cfg := testOrchConfig()

	tr := &stubTrackerForOrch{
		byIDs: []tracker.Issue{
			{ID: "1", State: "in_progress", Title: "Updated Title"},
		},
	}
	o := New(tr, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	o.State.SetRunning("1", &RunInfo{
		Issue:        tracker.Issue{ID: "1", State: "in_progress", Title: "Old Title"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})

	o.reconcileRunning(context.Background(), cfg)

	// Issue should still be running.
	if !o.State.IsRunning("1") {
		t.Error("active issue should still be running after reconcile")
	}

	// Issue snapshot should be updated.
	info := o.State.RunningIssue("1")
	if info.Issue.Title != "Updated Title" {
		t.Errorf("issue title = %q, want %q", info.Issue.Title, "Updated Title")
	}
}

func TestReconcile_NonActiveNonTerminal_TerminatesWithoutCleanup(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Workspace.Root = t.TempDir()

	tr := &stubTrackerForOrch{
		byIDs: []tracker.Issue{
			{ID: "1", State: "In Review", Title: "Review Title"},
		},
	}
	o := New(tr, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	wsPath := cfg.Workspace.Root + "/test-issue"
	o.State.SetRunning("1", &RunInfo{
		Issue:         tracker.Issue{ID: "1", State: "in_progress"},
		WorkspacePath: wsPath,
		StartedAt:     time.Now(),
		LastActivity:  time.Now(),
	})

	o.reconcileRunning(context.Background(), cfg)

	// Issue should no longer be running.
	if o.State.IsRunning("1") {
		t.Error("non-active non-terminal issue should be removed from running")
	}

	// Claim should be released.
	if o.State.IsClaimed("1") {
		t.Error("claim should be released")
	}
}

func TestReconcile_TerminalState_WithCleanup(t *testing.T) {
	cfg := testOrchConfig()
	cfg.Workspace.Root = t.TempDir()

	tr := &stubTrackerForOrch{
		byIDs: []tracker.Issue{
			{ID: "1", State: "done", Title: "Done Title"},
		},
	}
	o := New(tr, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	wsPath := cfg.Workspace.Root + "/test-issue"
	o.State.SetRunning("1", &RunInfo{
		Issue:         tracker.Issue{ID: "1", State: "in_progress"},
		WorkspacePath: wsPath,
		StartedAt:     time.Now(),
		LastActivity:  time.Now(),
	})

	o.reconcileRunning(context.Background(), cfg)

	if o.State.IsRunning("1") {
		t.Error("terminal issue should be removed from running")
	}
}

func TestReconcile_BatchFetch(t *testing.T) {
	cfg := testOrchConfig()

	tr := &stubTrackerForOrch{
		byIDs: []tracker.Issue{
			{ID: "1", State: "in_progress"},
			{ID: "2", State: "done"},
			{ID: "3", State: "In Review"},
		},
	}
	o := New(tr, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	o.State.SetRunning("1", &RunInfo{
		Issue:        tracker.Issue{ID: "1", State: "in_progress"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})
	o.State.SetRunning("2", &RunInfo{
		Issue:        tracker.Issue{ID: "2", State: "in_progress"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})
	o.State.SetRunning("3", &RunInfo{
		Issue:        tracker.Issue{ID: "3", State: "in_progress"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})

	o.reconcileRunning(context.Background(), cfg)

	// Issue 1 (active) should still be running.
	if !o.State.IsRunning("1") {
		t.Error("active issue should still be running")
	}
	// Issue 2 (terminal) should be removed.
	if o.State.IsRunning("2") {
		t.Error("terminal issue should be removed")
	}
	// Issue 3 (neither) should be removed.
	if o.State.IsRunning("3") {
		t.Error("non-active non-terminal issue should be removed")
	}
}

func TestUpdateUsage_DeltaTracking(t *testing.T) {
	s := NewState()
	s.SetRunning("1", &RunInfo{
		Issue:        tracker.Issue{ID: "1"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})

	// First report: per-turn delta (agent reports turn usage).
	s.UpdateUsage("1", agent.UsageReport{InputTokens: 100, OutputTokens: 50, TotalTokens: 150})

	info := s.RunningIssue("1")
	if info.TotalUsage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", info.TotalUsage.InputTokens)
	}
	if info.TotalUsage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", info.TotalUsage.OutputTokens)
	}

	// Second report: cumulative totals from agent (200 input, 100 output).
	// Delta should be 100, 50 respectively.
	s.UpdateUsage("1", agent.UsageReport{InputTokens: 200, OutputTokens: 100, TotalTokens: 300})

	info = s.RunningIssue("1")
	if info.TotalUsage.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200", info.TotalUsage.InputTokens)
	}
	if info.TotalUsage.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want 100", info.TotalUsage.OutputTokens)
	}
	if info.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", info.TurnCount)
	}

	// Third report: same as second (no increase) — delta should be 0.
	s.UpdateUsage("1", agent.UsageReport{InputTokens: 200, OutputTokens: 100, TotalTokens: 300})

	info = s.RunningIssue("1")
	if info.TotalUsage.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200 (no increase)", info.TotalUsage.InputTokens)
	}
	if info.TurnCount != 3 {
		t.Errorf("TurnCount = %d, want 3", info.TurnCount)
	}
}

func TestSetPhase(t *testing.T) {
	s := NewState()
	s.SetRunning("1", &RunInfo{
		Issue:        tracker.Issue{ID: "1"},
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	})

	s.SetPhase("1", PhaseCreatingWorkspace)
	info := s.RunningIssue("1")
	if info.Phase != PhaseCreatingWorkspace {
		t.Errorf("Phase = %q, want %q", info.Phase, PhaseCreatingWorkspace)
	}

	s.SetPhase("1", PhaseRunningTurns)
	info = s.RunningIssue("1")
	if info.Phase != PhaseRunningTurns {
		t.Errorf("Phase = %q, want %q", info.Phase, PhaseRunningTurns)
	}
}

func TestScheduleRetry_ContinuationAttemptReset(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	// Simulate a run that was attempt 3 and completed (continuation).
	info := &RunInfo{
		Issue:        tracker.Issue{ID: "1", State: "in_progress"},
		Attempt:      3,
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	}

	o.scheduleRetry("1", info, true, cfg)

	entry, ok := o.State.RemoveRetry("1")
	if !ok {
		t.Fatal("expected retry entry")
	}
	// Continuation retry should reset attempt to 1.
	if entry.Attempt != 1 {
		t.Errorf("continuation retry attempt = %d, want 1", entry.Attempt)
	}
}

func TestScheduleRetry_FailureAttemptIncrement(t *testing.T) {
	cfg := testOrchConfig()
	o := New(nil, nil, nil, func() config.Schema { return cfg }, func() string { return "test" })

	info := &RunInfo{
		Issue:        tracker.Issue{ID: "1", State: "in_progress"},
		Attempt:      2,
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
	}

	o.scheduleRetry("1", info, false, cfg)

	entry, ok := o.State.RemoveRetry("1")
	if !ok {
		t.Fatal("expected retry entry")
	}
	// Failure retry should increment attempt.
	if entry.Attempt != 3 {
		t.Errorf("failure retry attempt = %d, want 3", entry.Attempt)
	}
}
