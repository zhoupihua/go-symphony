package ha

import "context"

// Elector provides leader election for high-availability deployments.
type Elector interface {
	// Campaign blocks until this instance becomes leader or the context is cancelled.
	Campaign(ctx context.Context) error

	// IsLeader returns true if this instance currently holds leadership.
	IsLeader() bool

	// Resign voluntarily relinquishes leadership.
	Resign()

	// Done returns a channel that is closed when the elector shuts down.
	Done() <-chan struct{}

	// LeaderAddr returns the address of the current leader.
	LeaderAddr() string
}
