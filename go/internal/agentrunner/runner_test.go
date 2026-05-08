package agentrunner

import (
	"context"
	"testing"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/tracker"
)

// stubAgent implements agent.Agent for testing. It returns a pre-configured
// session from the sessionFactory function.
type stubAgent struct {
	sessionFactory func(agent.SessionOptions) agent.Session
}

func (a *stubAgent) StartSession(_ context.Context, opts agent.SessionOptions) (agent.Session, error) {
	if a.sessionFactory != nil {
		return a.sessionFactory(opts), nil
	}
	return &stubSession{}, nil
}

type stubSession struct {
	turns   []stubTurnResult
	callIdx int
	closed  bool
}

type stubTurnResult struct {
	result agent.TurnResult
	err    error
}

func (s *stubSession) RunTurn(_ context.Context, _ string, _ agent.TurnOptions) (agent.TurnResult, error) {
	if s.callIdx >= len(s.turns) {
		return agent.TurnResult{}, context.DeadlineExceeded
	}
	r := s.turns[s.callIdx]
	s.callIdx++
	return r.result, r.err
}

func (s *stubSession) Close() error {
	s.closed = true
	return nil
}

// stubTrackerImpl implements tracker.Tracker for testing.
type stubTrackerImpl struct {
	issuesByState []tracker.Issue
	err           error
}

func (t *stubTrackerImpl) FetchCandidateIssues(_ context.Context) ([]tracker.Issue, error) {
	return nil, nil
}
func (t *stubTrackerImpl) FetchIssuesByStates(_ context.Context, _ []string) ([]tracker.Issue, error) {
	return nil, nil
}
func (t *stubTrackerImpl) FetchIssueStatesByIDs(_ context.Context, _ []string) ([]tracker.Issue, error) {
	return t.issuesByState, t.err
}
func (t *stubTrackerImpl) CreateComment(_ context.Context, _, _ string) error { return nil }
func (t *stubTrackerImpl) UpdateIssueState(_ context.Context, _, _ string) error {
	return nil
}

func testConfig(t *testing.T) config.Schema {
	return config.Schema{
		Workspace: config.WorkspaceConfig{
			Root: t.TempDir(),
		},
		Agent: config.AgentConfig{
			Kind:     "codex",
			MaxTurns: 3,
		},
		Tracker: config.TrackerConfig{
			ActiveStates: []string{"in_progress", "todo"},
		},
	}
}

func TestRunnerSingleTurnCompletion(t *testing.T) {
	cfg := testConfig(t)
	sess := &stubSession{
		turns: []stubTurnResult{
			{
				result: agent.TurnResult{
					Completed: true,
					Usage:     agent.UsageReport{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
					Output:    "Fixed the bug",
				},
			},
		},
	}

	r := &Runner{
		Agent: &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Cfg:   cfg,
		Tmpl:  "Fix the issue: {{.Issue.Title}}",
	}

	eventCh := make(chan agent.Event, 10)
	result, err := r.Run(context.Background(), tracker.Issue{
		ID:         "iss-1",
		Identifier: "PROJ-1",
		Title:      "Bug in auth",
		State:      "in_progress",
	}, 1, eventCh)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
	if result.TotalTurns != 1 {
		t.Errorf("TotalTurns = %d, want 1", result.TotalTurns)
	}
	if result.FinalOutput != "Fixed the bug" {
		t.Errorf("FinalOutput = %q, want %q", result.FinalOutput, "Fixed the bug")
	}
	if result.TotalUsage.InputTokens != 100 {
		t.Errorf("TotalUsage.InputTokens = %d, want 100", result.TotalUsage.InputTokens)
	}

	select {
	case evt := <-eventCh:
		if evt.Type != agent.EventTurnCompleted {
			t.Errorf("Event type = %q, want %q", evt.Type, agent.EventTurnCompleted)
		}
	default:
		t.Error("expected event on eventCh")
	}
}

func TestRunnerMultipleTurns(t *testing.T) {
	cfg := testConfig(t)
	sess := &stubSession{
		turns: []stubTurnResult{
			{
				result: agent.TurnResult{
					Completed: false,
					Usage:     agent.UsageReport{InputTokens: 50, OutputTokens: 100, TotalTokens: 150},
				},
			},
			{
				result: agent.TurnResult{
					Completed: true,
					Usage:     agent.UsageReport{InputTokens: 60, OutputTokens: 110, TotalTokens: 170},
					Output:    "Done",
				},
			},
		},
	}

	r := &Runner{
		Agent: &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Cfg:   cfg,
		Tmpl:  "Fix: {{.Issue.Title}}",
	}

	result, err := r.Run(context.Background(), tracker.Issue{
		ID:    "iss-2",
		Title: "Multi-turn issue",
		State: "in_progress",
	}, 1, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
	if result.TotalTurns != 2 {
		t.Errorf("TotalTurns = %d, want 2", result.TotalTurns)
	}
	if result.TotalUsage.InputTokens != 110 {
		t.Errorf("TotalUsage.InputTokens = %d, want 110", result.TotalUsage.InputTokens)
	}
}

func TestRunnerTurnError(t *testing.T) {
	cfg := testConfig(t)
	sess := &stubSession{
		turns: []stubTurnResult{
			{err: context.DeadlineExceeded},
		},
	}

	r := &Runner{
		Agent: &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Cfg:   cfg,
		Tmpl:  "Fix: {{.Issue.Title}}",
	}

	_, err := r.Run(context.Background(), tracker.Issue{
		ID:    "iss-3",
		Title: "Failing issue",
		State: "in_progress",
	}, 1, nil)
	if err == nil {
		t.Fatal("Run() should return error on turn failure")
	}
}

func TestRunnerContextCancellation(t *testing.T) {
	cfg := testConfig(t)
	sess := &stubSession{
		turns: []stubTurnResult{
			{err: context.DeadlineExceeded},
		},
	}

	r := &Runner{
		Agent: &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Cfg:   cfg,
		Tmpl:  "Fix: {{.Issue.Title}}",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := r.Run(ctx, tracker.Issue{
		ID:    "iss-4",
		Title: "Timeout issue",
		State: "in_progress",
	}, 1, nil)
	if err == nil {
		t.Fatal("Run() should return error on context cancellation")
	}
}

func TestRunnerIssueLeavesActiveState(t *testing.T) {
	cfg := testConfig(t)
	st := &stubTrackerImpl{
		issuesByState: []tracker.Issue{
			{ID: "iss-5", State: "done"}, // no longer in active states
		},
	}
	sess := &stubSession{
		turns: []stubTurnResult{
			{
				result: agent.TurnResult{
					Completed: false,
					Usage:     agent.UsageReport{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
				},
			},
		},
	}

	r := &Runner{
		Agent:   &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Tracker: st,
		Cfg:     cfg,
		Tmpl:    "Fix: {{.Issue.Title}}",
	}

	result, err := r.Run(context.Background(), tracker.Issue{
		ID:    "iss-5",
		Title: "State changed issue",
		State: "in_progress",
	}, 1, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The first turn runs, then the state check stops the loop.
	if result.Completed {
		t.Error("Completed = true, want false (issue left active state)")
	}
	if result.TotalTurns != 1 {
		t.Errorf("TotalTurns = %d, want 1 (ran one turn, then stopped by state check)", result.TotalTurns)
	}
}

func TestRunnerMaxTurns(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agent.MaxTurns = 2
	sess := &stubSession{
		turns: []stubTurnResult{
			{result: agent.TurnResult{Completed: false, Usage: agent.UsageReport{InputTokens: 10}}},
			{result: agent.TurnResult{Completed: false, Usage: agent.UsageReport{InputTokens: 10}}},
			{result: agent.TurnResult{Completed: true, Output: "Should not reach"}},
		},
	}

	r := &Runner{
		Agent: &stubAgent{sessionFactory: func(_ agent.SessionOptions) agent.Session { return sess }},
		Cfg:   cfg,
		Tmpl:  "Fix: {{.Issue.Title}}",
	}

	result, err := r.Run(context.Background(), tracker.Issue{
		ID:    "iss-6",
		Title: "Max turns issue",
		State: "in_progress",
	}, 1, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Completed {
		t.Error("Completed = true, want false (hit max turns)")
	}
	if result.TotalTurns != 2 {
		t.Errorf("TotalTurns = %d, want 2 (max turns limit)", result.TotalTurns)
	}
}

func TestRunnerBeforeRunHookFailure(t *testing.T) {
	cfg := testConfig(t)
	cfg.Hooks.BeforeRun = "exit 1"
	cfg.Hooks.TimeoutMS = 5000

	r := &Runner{
		Cfg:   cfg,
		Tmpl:  "Fix: {{.Issue.Title}}",
		Agent: &stubAgent{},
	}

	_, err := r.Run(context.Background(), tracker.Issue{
		ID:         "iss-7",
		Identifier: "PROJ-7",
		Title:      "Hook fail issue",
		State:      "in_progress",
	}, 1, nil)
	if err == nil {
		t.Fatal("Run() should return error when before_run hook fails")
	}
}

func TestAgentConfigMapCodex(t *testing.T) {
	cfg := config.Schema{
		Agent: config.AgentConfig{
			Kind: "codex",
			Codex: config.CodexConfig{
				Command:        "codex app-server --model gpt-4",
				ApprovalPolicy: "suggest",
				TurnTimeoutMS:  60000,
			},
		},
	}

	m := agentConfigMap(cfg)
	if m["command"] != "codex app-server --model gpt-4" {
		t.Errorf("command = %v, want codex app-server --model gpt-4", m["command"])
	}
	if m["approval_policy"] != "suggest" {
		t.Errorf("approval_policy = %v, want suggest", m["approval_policy"])
	}
	if m["turn_timeout_ms"] != float64(60000) {
		t.Errorf("turn_timeout_ms = %v, want 60000", m["turn_timeout_ms"])
	}
}

func TestAgentConfigMapClaude(t *testing.T) {
	cfg := config.Schema{
		Agent: config.AgentConfig{
			Kind: "claude",
			Claude: config.ClaudeConfig{
				Command:        "claude --model sonnet",
				PermissionMode: "auto",
				AllowedTools:   []string{"Read", "Write"},
			},
		},
	}

	m := agentConfigMap(cfg)
	if m["command"] != "claude --model sonnet" {
		t.Errorf("command = %v, want claude --model sonnet", m["command"])
	}
	if m["permission_mode"] != "auto" {
		t.Errorf("permission_mode = %v, want auto", m["permission_mode"])
	}
}

func TestHookTimeoutDefault(t *testing.T) {
	cfg := config.Schema{}
	d := hookTimeout(cfg)
	if d != 5*time.Minute {
		t.Errorf("hookTimeout = %v, want 5m", d)
	}
}

func TestHookTimeoutConfigured(t *testing.T) {
	cfg := config.Schema{
		Hooks: config.HooksConfig{TimeoutMS: 30000},
	}
	d := hookTimeout(cfg)
	if d != 30*time.Second {
		t.Errorf("hookTimeout = %v, want 30s", d)
	}
}
