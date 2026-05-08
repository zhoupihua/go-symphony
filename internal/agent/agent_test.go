package agent

import (
	"context"
	"testing"

	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// stubAgent and stubSession implement Agent and Session for registry tests.
type stubAgent struct{}

func (stubAgent) StartSession(_ context.Context, _ SessionOptions) (Session, error) {
	return stubSession{}, nil
}

type stubSession struct{}

func (stubSession) RunTurn(_ context.Context, _ string, _ TurnOptions) (TurnResult, error) {
	return TurnResult{}, nil
}
func (stubSession) Close() error { return nil }

func TestRegisterAndNewAgent(t *testing.T) {
	orig := agentRegistry
	agentRegistry = map[string]AgentFactory{}
	defer func() { agentRegistry = orig }()

	RegisterAgent("test", func(_ map[string]any) (Agent, error) {
		return stubAgent{}, nil
	})

	ag, err := NewAgent("test", nil)
	if err != nil {
		t.Fatalf("NewAgent returned error: %v", err)
	}
	if _, ok := ag.(stubAgent); !ok {
		t.Fatalf("expected stubAgent, got %T", ag)
	}

	// Verify the agent can create a session using the tracker import.
	sess, err := ag.StartSession(context.Background(), SessionOptions{
		Issue: tracker.Issue{ID: "1", Identifier: "T-1"},
	})
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}
	_ = sess.Close()
}

func TestNewAgentUnknownKind(t *testing.T) {
	orig := agentRegistry
	agentRegistry = map[string]AgentFactory{}
	defer func() { agentRegistry = orig }()

	_, err := NewAgent("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

func TestRegisterAgentDuplicatePanics(t *testing.T) {
	orig := agentRegistry
	agentRegistry = map[string]AgentFactory{}
	defer func() { agentRegistry = orig }()

	factory := func(_ map[string]any) (Agent, error) { return stubAgent{}, nil }
	RegisterAgent("dup", factory)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration, got none")
		}
	}()
	RegisterAgent("dup", factory)
}
