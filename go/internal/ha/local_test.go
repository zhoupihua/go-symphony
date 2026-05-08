package ha

import (
	"context"
	"testing"
	"time"
)

func TestLocalElectorImplementsInterface(t *testing.T) {
	// Compile-time check is in local.go; this test verifies runtime behavior.
	var e Elector = NewLocalElector()
	_ = e
}

func TestLocalElectorCampaignReturnsImmediately(t *testing.T) {
	e := NewLocalElector()

	start := time.Now()
	err := e.Campaign(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Campaign() returned error: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Campaign() took %v, expected immediate return", elapsed)
	}
}

func TestLocalElectorIsLeader(t *testing.T) {
	e := NewLocalElector()

	if !e.IsLeader() {
		t.Error("IsLeader() = false, expected true")
	}
}

func TestLocalElectorResignIsNoOp(t *testing.T) {
	e := NewLocalElector()

	e.Resign()

	// After resign, IsLeader should still be true.
	if !e.IsLeader() {
		t.Error("IsLeader() = false after Resign(), expected true")
	}
}

func TestLocalElectorDoneNeverCloses(t *testing.T) {
	e := NewLocalElector()

	select {
	case <-e.Done():
		t.Error("Done() channel should not close")
	case <-time.After(50 * time.Millisecond):
		// Expected: channel remains open.
	}
}

func TestLocalElectorLeaderAddr(t *testing.T) {
	e := NewLocalElector()

	if addr := e.LeaderAddr(); addr != "" {
		t.Errorf("LeaderAddr() = %q, expected empty string", addr)
	}
}

func TestLocalElectorCampaignRespectsContextCancellation(t *testing.T) {
	e := NewLocalElector()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := e.Campaign(ctx)
	if err != nil {
		t.Errorf("Campaign() with cancelled context returned error: %v", err)
	}
}
