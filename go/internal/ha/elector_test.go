package ha

import (
	"context"
	"testing"
)

// mockElector is a minimal implementation of Elector for compile-time verification.
type mockElector struct{}

func (mockElector) Campaign(_ context.Context) error { return nil }
func (mockElector) IsLeader() bool                   { return false }
func (mockElector) Resign()                          {}
func (mockElector) Done() <-chan struct{}             { return nil }
func (mockElector) LeaderAddr() string               { return "" }

// Compile-time checks.
var (
	_ Elector        = mockElector{}
	_ StateReplicator = (*RaftElector)(nil)
	_ ClusterManager  = (*RaftElector)(nil)
	_ StateReplicator = (*LocalElector)(nil)
	_ ClusterManager  = (*LocalElector)(nil)
)

func TestElectorInterfaceSatisfied(t *testing.T) {
	var e Elector = mockElector{}
	if e.IsLeader() {
		t.Error("mockElector should not be leader by default")
	}
}

func TestLocalElectorImplementsStateReplicator(t *testing.T) {
	var sr StateReplicator = NewLocalElector()
	if err := sr.ApplyCommand("test", "key", nil); err != nil {
		t.Errorf("LocalElector.ApplyCommand() = %v, want nil", err)
	}
	data, err := sr.ReplicatedState()
	if err != nil {
		t.Errorf("LocalElector.ReplicatedState() = %v, want nil", err)
	}
	if data != nil {
		t.Errorf("LocalElector.ReplicatedState() = %v, want nil", data)
	}
}

func TestLocalElectorImplementsClusterManager(t *testing.T) {
	var cm ClusterManager = NewLocalElector()

	if err := cm.AddVoter(context.Background(), "id", "addr"); err == nil {
		t.Error("LocalElector.AddVoter() should return error")
	}
	if err := cm.RemoveServer(context.Background(), "id"); err == nil {
		t.Error("LocalElector.RemoveServer() should return error")
	}
	members, err := cm.GetConfiguration()
	if err != nil {
		t.Errorf("LocalElector.GetConfiguration() = %v, want nil", err)
	}
	if len(members) != 1 || !members[0].IsLeader {
		t.Errorf("LocalElector.GetConfiguration() = %v, want 1 leader member", members)
	}
}
