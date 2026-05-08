package ha

import "context"

// Compile-time interface check.
var _ Elector = (*LocalElector)(nil)

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
