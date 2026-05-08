package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// RaftConfig holds configuration for the Raft-based elector.
type RaftConfig struct {
	Peers         []string // Raft peer addresses (host:port format)
	AdvertiseAddr string   // Address this instance advertises as leader (HTTP addr)
	RaftDir       string   // Directory for Raft log and snapshot storage
	RaftBindPort  int      // Port for Raft transport (0 = derive from peers)
}

// FSM command operations.
const (
	OpSetLeaderAddr = "set_leader_addr"
	OpSetRunning    = "set_running"
	OpRemoveRunning = "remove_running"
	OpClaim         = "claim"
	OpReleaseClaim  = "release_claim"
	OpAddRetry      = "add_retry"
	OpRemoveRetry   = "remove_retry"
	OpMarkCompleted = "mark_completed"
	OpSnapshotState = "snapshot_state"
)

// fsmCommand is a generic Raft FSM command.
type fsmCommand struct {
	Op   string          `json:"op"`
	Key  string          `json:"key,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// fsmState is the full replicated state maintained by the FSM.
type fsmState struct {
	LeaderAddr     string                     `json:"leader_addr"`
	Running        map[string]json.RawMessage `json:"running"`
	Claimed        map[string]bool            `json:"claimed"`
	Retries        map[string]json.RawMessage `json:"retries"`
	Completed      map[string]bool            `json:"completed"`
	TotalRuntimeMS int64                      `json:"total_runtime_ms"`
}

func newFSMState() fsmState {
	return fsmState{
		Running:   make(map[string]json.RawMessage),
		Claimed:   make(map[string]bool),
		Retries:   make(map[string]json.RawMessage),
		Completed: make(map[string]bool),
	}
}

// replicatedFSM is a Raft FSM that stores full orchestrator state.
type replicatedFSM struct {
	mu    sync.RWMutex
	state fsmState
}

type fsmSnapshot struct {
	state fsmState
}

// Apply applies a Raft log entry to the FSM.
func (f *replicatedFSM) Apply(l *raft.Log) any {
	// Try new-format fsmCommand first.
	var cmd fsmCommand
	if err := json.Unmarshal(l.Data, &cmd); err == nil && cmd.Op != "" {
		f.mu.Lock()
		f.applyCommand(cmd)
		f.mu.Unlock()
		return nil
	}

	// Backward compat: old-format leaderData.
	var d struct {
		AdvertiseAddr string `json:"advertise_addr"`
	}
	if err := json.Unmarshal(l.Data, &d); err != nil {
		slog.Error("raft FSM: unmarshal log entry", "error", err)
		return nil
	}
	f.mu.Lock()
	f.state.LeaderAddr = d.AdvertiseAddr
	f.mu.Unlock()
	return nil
}

func (f *replicatedFSM) applyCommand(cmd fsmCommand) {
	switch cmd.Op {
	case OpSetLeaderAddr:
		var addr string
		if err := json.Unmarshal(cmd.Data, &addr); err == nil {
			f.state.LeaderAddr = addr
		} else {
			f.state.LeaderAddr = string(cmd.Data)
		}
	case OpSetRunning:
		if f.state.Running == nil {
			f.state.Running = make(map[string]json.RawMessage)
		}
		f.state.Running[cmd.Key] = cmd.Data
	case OpRemoveRunning:
		delete(f.state.Running, cmd.Key)
	case OpClaim:
		if f.state.Claimed == nil {
			f.state.Claimed = make(map[string]bool)
		}
		f.state.Claimed[cmd.Key] = true
	case OpReleaseClaim:
		delete(f.state.Claimed, cmd.Key)
	case OpAddRetry:
		if f.state.Retries == nil {
			f.state.Retries = make(map[string]json.RawMessage)
		}
		f.state.Retries[cmd.Key] = cmd.Data
	case OpRemoveRetry:
		delete(f.state.Retries, cmd.Key)
	case OpMarkCompleted:
		if f.state.Completed == nil {
			f.state.Completed = make(map[string]bool)
		}
		f.state.Completed[cmd.Key] = true
	case OpSnapshotState:
		var s fsmState
		if err := json.Unmarshal(cmd.Data, &s); err == nil {
			f.state = s
		}
	}
}

// Snapshot returns a snapshot of the FSM.
func (f *replicatedFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return &fsmSnapshot{state: f.state}, nil
}

// Restore restores the FSM from a snapshot.
func (f *replicatedFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var s fsmState
	if err := json.NewDecoder(rc).Decode(&s); err != nil {
		return fmt.Errorf("raft FSM restore: %w", err)
	}
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
	return nil
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	b, err := json.Marshal(s.state)
	if err != nil {
		sink.Cancel()
		return fmt.Errorf("raft snapshot marshal: %w", err)
	}
	if _, err := sink.Write(b); err != nil {
		sink.Cancel()
		return fmt.Errorf("raft snapshot write: %w", err)
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}

// RaftElector implements the Elector interface using hashicorp/raft.
type RaftElector struct {
	cfg               RaftConfig
	raft              *raft.Raft
	fsm               *replicatedFSM
	logStore          *raftboltdb.BoltStore
	transport         raft.Transport
	mu                sync.RWMutex
	leader            bool
	becameLeader      chan struct{} // signaled by watchLeadership when this node becomes leader
	done              chan struct{} // closed when leadership is lost
	closed            bool
	leaderWatcherDone chan struct{}
}

// Compile-time interface checks.
var (
	_ Elector        = (*RaftElector)(nil)
	_ StateReplicator = (*RaftElector)(nil)
	_ ClusterManager  = (*RaftElector)(nil)
)

// NewRaftElector creates a new Raft-based elector.
func NewRaftElector(cfg RaftConfig) (*RaftElector, error) {
	if len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("raft_peers is required")
	}
	if cfg.AdvertiseAddr == "" {
		return nil, fmt.Errorf("advertise_addr is required")
	}
	if cfg.RaftDir == "" {
		return nil, fmt.Errorf("raft_dir is required")
	}

	// Create raft data directory.
	if err := os.MkdirAll(cfg.RaftDir, 0700); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}

	// Determine this node's Raft address.
	raftAddr, err := resolveRaftAddr(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve raft address: %w", err)
	}

	// Create transport.
	transport, err := raft.NewTCPTransport(raftAddr, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create raft transport: %w", err)
	}

	// Create snapshot store.
	snapshots, err := raft.NewFileSnapshotStore(cfg.RaftDir, 2, os.Stderr)
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	// Create BoltDB log store and stable store.
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.RaftDir, "raft.db"))
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("create bolt store: %w", err)
	}

	// Create FSM.
	fsm := &replicatedFSM{state: newFSMState()}

	// Raft config.
	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(raftAddr)
	raftCfg.SnapshotInterval = 30 * time.Second
	raftCfg.SnapshotThreshold = 2

	// Create Raft instance.
	r, err := raft.NewRaft(raftCfg, fsm, logStore, logStore, snapshots, transport)
	if err != nil {
		logStore.Close()
		transport.Close()
		return nil, fmt.Errorf("create raft: %w", err)
	}

	// Bootstrap cluster if this is a fresh start.
	configuration := r.GetConfiguration()
	if configuration.Error() != nil {
		r.Shutdown()
		logStore.Close()
		transport.Close()
		return nil, fmt.Errorf("get raft configuration: %w", configuration.Error())
	}

	if len(configuration.Configuration().Servers) == 0 {
		var servers []raft.Server
		for _, peer := range cfg.Peers {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(peer),
				Address: raft.ServerAddress(peer),
			})
		}
		future := r.BootstrapCluster(raft.Configuration{Servers: servers})
		if err := future.Error(); err != nil && err != raft.ErrCantBootstrap {
			slog.Warn("raft bootstrap (may be expected if already bootstrapped)", "error", err)
		}
	}

	e := &RaftElector{
		cfg:               cfg,
		raft:              r,
		fsm:               fsm,
		logStore:          logStore,
		transport:         transport,
		becameLeader:      make(chan struct{}, 1),
		done:              make(chan struct{}),
		leaderWatcherDone: make(chan struct{}),
	}

	// watchLeadership is the sole reader of LeaderCh — Campaign must not read it.
	go e.watchLeadership()

	return e, nil
}

// watchLeadership is the sole consumer of raft.LeaderCh(). It handles both
// becoming leader and losing leadership, and signals Campaign via becameLeader.
func (e *RaftElector) watchLeadership() {
	for {
		select {
		case <-e.leaderWatcherDone:
			return
		case isLeader := <-e.raft.LeaderCh():
			e.mu.Lock()
			if isLeader {
				e.leader = true
				// Signal Campaign (non-blocking: channel has buffer 1).
				select {
				case e.becameLeader <- struct{}{}:
				default:
				}
			} else if e.leader && !e.closed {
				e.leader = false
				close(e.done)
				e.done = make(chan struct{})
			}
			e.mu.Unlock()
		}
	}
}

// Campaign blocks until this instance becomes the Raft leader.
func (e *RaftElector) Campaign(ctx context.Context) error {
	// If already leader, return immediately.
	if e.raft.State() == raft.Leader {
		return nil
	}

	// Wait for leadership signal from watchLeadership.
	for {
		select {
		case <-e.becameLeader:
			// Store our advertise address in the FSM.
			e.storeLeaderData()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// IsLeader returns whether this instance is the Raft leader.
func (e *RaftElector) IsLeader() bool {
	return e.raft.State() == raft.Leader
}

// Resign steps down as leader.
func (e *RaftElector) Resign() {
	if e.raft.State() != raft.Leader {
		return
	}

	// Transfer leadership to another peer.
	future := e.raft.LeadershipTransfer()
	if err := future.Error(); err != nil {
		slog.Warn("raft leadership transfer failed", "error", err)
	}

	e.mu.Lock()
	e.leader = false
	if !e.closed {
		close(e.done)
		e.done = make(chan struct{})
	}
	e.mu.Unlock()
}

// Done returns a channel that is closed when leadership is lost.
func (e *RaftElector) Done() <-chan struct{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.done
}

// LeaderAddr returns the advertise address of the current Raft leader.
func (e *RaftElector) LeaderAddr() string {
	if e.raft.State() == raft.Leader {
		return e.cfg.AdvertiseAddr
	}
	e.fsm.mu.RLock()
	defer e.fsm.mu.RUnlock()
	return e.fsm.state.LeaderAddr
}

// ApplyCommand submits a state mutation to the Raft log.
func (e *RaftElector) ApplyCommand(op, key string, data []byte) error {
	if e.raft.State() != raft.Leader {
		return fmt.Errorf("not leader: only the leader can apply commands")
	}
	cmd := fsmCommand{Op: op, Key: key, Data: json.RawMessage(data)}
	b, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	future := e.raft.Apply(b, 5*time.Second)
	return future.Error()
}

// ReplicatedState returns a full snapshot of the replicated state.
func (e *RaftElector) ReplicatedState() ([]byte, error) {
	e.fsm.mu.RLock()
	defer e.fsm.mu.RUnlock()
	return json.Marshal(e.fsm.state)
}

// AddVoter adds a new voter to the Raft cluster.
func (e *RaftElector) AddVoter(ctx context.Context, id, addr string) error {
	if e.raft.State() != raft.Leader {
		return fmt.Errorf("not leader: only the leader can add voters")
	}
	future := e.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 5*time.Second)
	return future.Error()
}

// RemoveServer removes a server from the Raft cluster.
func (e *RaftElector) RemoveServer(ctx context.Context, id string) error {
	if e.raft.State() != raft.Leader {
		return fmt.Errorf("not leader: only the leader can remove servers")
	}
	future := e.raft.RemoveServer(raft.ServerID(id), 0, 5*time.Second)
	return future.Error()
}

// GetConfiguration returns the current cluster configuration.
func (e *RaftElector) GetConfiguration() ([]ClusterMember, error) {
	future := e.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("get configuration: %w", err)
	}

	servers := future.Configuration().Servers
	leaderAddr := string(e.raft.Leader())

	members := make([]ClusterMember, len(servers))
	for i, s := range servers {
		members[i] = ClusterMember{
			ID:       string(s.ID),
			Address:  string(s.Address),
			IsLeader: string(s.Address) == leaderAddr,
		}
	}
	return members, nil
}

// Close shuts down the Raft instance and closes the BoltDB store.
func (e *RaftElector) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.done)
	e.mu.Unlock()

	// Stop the leadership watcher.
	close(e.leaderWatcherDone)

	future := e.raft.Shutdown()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft shutdown: %w", err)
	}

	if e.logStore != nil {
		if err := e.logStore.Close(); err != nil {
			return fmt.Errorf("close bolt store: %w", err)
		}
	}

	if e.transport != nil {
		if closer, ok := e.transport.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				return fmt.Errorf("close transport: %w", err)
			}
		}
	}

	return nil
}

// storeLeaderData writes the leader's advertise address to the FSM via Raft.
func (e *RaftElector) storeLeaderData() {
	data, _ := json.Marshal(e.cfg.AdvertiseAddr)
	cmd := fsmCommand{Op: OpSetLeaderAddr, Data: json.RawMessage(data)}
	b, err := json.Marshal(cmd)
	if err != nil {
		slog.Error("raft: marshal leader data", "error", err)
		return
	}

	future := e.raft.Apply(b, 5*time.Second)
	if err := future.Error(); err != nil {
		slog.Error("raft: apply leader data", "error", err)
	}
}

// resolveRaftAddr determines this node's Raft transport address from the
// peers list. It finds the peer that matches the AdvertiseAddr's host.
func resolveRaftAddr(cfg RaftConfig) (string, error) {
	host, _, err := net.SplitHostPort(cfg.AdvertiseAddr)
	if err != nil {
		host = cfg.AdvertiseAddr
	}

	for _, peer := range cfg.Peers {
		peerHost, _, err := net.SplitHostPort(peer)
		if err != nil {
			peerHost = peer
		}
		if peerHost == host {
			return peer, nil
		}
	}

	if len(cfg.Peers) == 1 {
		return cfg.Peers[0], nil
	}

	return "", fmt.Errorf("no raft peer matches advertise host %q in peers %v", host, cfg.Peers)
}
