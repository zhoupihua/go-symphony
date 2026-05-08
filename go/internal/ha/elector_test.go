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

// Compile-time check: mockElector satisfies the Elector interface.
var _ Elector = mockElector{}

func TestElectorInterfaceSatisfied(t *testing.T) {
	// The var declaration above provides the compile-time check.
	// This test exists so the test runner reports a visible pass.
	var e Elector = mockElector{}
	if e.IsLeader() {
		t.Error("mockElector should not be leader by default")
	}
}
