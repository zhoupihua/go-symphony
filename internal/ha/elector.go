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

// StateReplicator replicates state mutations across the Raft cluster.
// Only the leader should call ApplyCommand; followers apply via FSM automatically.
type StateReplicator interface {
	// ApplyCommand submits a state mutation to the Raft log.
	// Returns error if this node is not the leader or if the apply times out.
	ApplyCommand(op, key string, data []byte) error

	// ReplicatedState returns a full snapshot of the replicated state.
	// Used by a new leader to restore orchestrator state on failover.
	ReplicatedState() ([]byte, error)
}

// ClusterMember represents a node in the Raft cluster.
type ClusterMember struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	IsLeader bool   `json:"is_leader"`
}

// ClusterManager manages dynamic cluster membership.
type ClusterManager interface {
	// AddVoter adds a new voter to the Raft cluster.
	AddVoter(ctx context.Context, id, addr string) error

	// RemoveServer removes a server from the Raft cluster.
	RemoveServer(ctx context.Context, id string) error

	// GetConfiguration returns the current cluster configuration.
	GetConfiguration() ([]ClusterMember, error)
}
