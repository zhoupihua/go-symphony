package ha

import (
	"context"
	"fmt"
)

// Compile-time interface checks.
var (
	_ Elector        = (*LocalElector)(nil)
	_ StateReplicator = (*LocalElector)(nil)
	_ ClusterManager  = (*LocalElector)(nil)
)

// LocalElector is a single-instance elector that always assumes leadership.
// Use it for non-HA deployments where only one instance runs.
type LocalElector struct {
	done chan struct{}
}

// NewLocalElector creates a LocalElector that is always leader.
func NewLocalElector() *LocalElector {
	return &LocalElector{
		done: make(chan struct{}),
	}
}

// Campaign returns immediately; the local instance is always leader.
func (l *LocalElector) Campaign(_ context.Context) error {
	return nil
}

// IsLeader always returns true for LocalElector.
func (l *LocalElector) IsLeader() bool {
	return true
}

// Resign is a no-op; the local instance cannot relinquish leadership.
func (l *LocalElector) Resign() {}

// Done returns a channel that never closes for LocalElector,
// since leadership is never lost.
func (l *LocalElector) Done() <-chan struct{} {
	return l.done
}

// LeaderAddr returns an empty string; there is no network address
// in a single-instance deployment.
func (l *LocalElector) LeaderAddr() string {
	return ""
}

// ApplyCommand is a no-op for LocalElector (no replication in non-HA mode).
func (l *LocalElector) ApplyCommand(_, _ string, _ []byte) error { return nil }

// ReplicatedState returns nil for LocalElector (no replicated state).
func (l *LocalElector) ReplicatedState() ([]byte, error) { return nil, nil }

// AddVoter returns an error for LocalElector (no cluster in non-HA mode).
func (l *LocalElector) AddVoter(_ context.Context, _, _ string) error {
	return fmt.Errorf("cluster management not available in non-HA mode")
}

// RemoveServer returns an error for LocalElector (no cluster in non-HA mode).
func (l *LocalElector) RemoveServer(_ context.Context, _ string) error {
	return fmt.Errorf("cluster management not available in non-HA mode")
}

// GetConfiguration returns a single-member list for LocalElector.
func (l *LocalElector) GetConfiguration() ([]ClusterMember, error) {
	return []ClusterMember{{ID: "local", Address: "local", IsLeader: true}}, nil
}
