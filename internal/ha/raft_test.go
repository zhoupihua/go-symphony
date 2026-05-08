package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// testNode holds a RaftElector and its test metadata.
type testNode struct {
	elector  *RaftElector
	raftAddr raft.ServerAddress
	httpAddr string
}

// createCluster creates a multi-node Raft cluster using in-memory transports.
func createCluster(t *testing.T, n int) []*testNode {
	t.Helper()

	baseDir, err := os.MkdirTemp("", "raft-cluster-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(baseDir) })

	type nodePrep struct {
		transport *raft.InmemTransport
		raftAddr  raft.ServerAddress
		httpAddr  string
		raftDir   string
	}

	preps := make([]nodePrep, n)
	for i := 0; i < n; i++ {
		addr, transport := raft.NewInmemTransport(raft.NewInmemAddr())
		preps[i] = nodePrep{
			transport: transport,
			raftAddr:  addr,
			httpAddr:  fmt.Sprintf("http-%d", i),
			raftDir:   filepath.Join(baseDir, fmt.Sprintf("node-%d", i)),
		}
	}

	// Build peers list from actual addresses.
	peers := make([]string, n)
	for i, p := range preps {
		peers[i] = string(p.raftAddr)
	}

	// Connect in-memory transports to each other.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				preps[i].transport.Connect(preps[j].raftAddr, preps[j].transport)
			}
		}
	}

	nodes := make([]*testNode, n)
	for i, p := range preps {
		e, err := newRaftElectorWithTransport(RaftConfig{
			Peers:         peers,
			AdvertiseAddr: p.httpAddr,
			RaftDir:       p.raftDir,
		}, p.transport)
		if err != nil {
			for j := 0; j < i; j++ {
				nodes[j].elector.Close()
			}
			t.Fatalf("NewRaftElector node %d: %v", i, err)
		}

		nodes[i] = &testNode{
			elector:  e,
			raftAddr: p.raftAddr,
			httpAddr: p.httpAddr,
		}
	}

	return nodes
}

// newRaftElectorWithTransport creates a RaftElector using a pre-created transport
// (for testing with in-memory transports).
func newRaftElectorWithTransport(cfg RaftConfig, transport raft.Transport) (*RaftElector, error) {
	if len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("raft_peers is required")
	}
	if cfg.AdvertiseAddr == "" {
		return nil, fmt.Errorf("advertise_addr is required")
	}
	if cfg.RaftDir == "" {
		return nil, fmt.Errorf("raft_dir is required")
	}

	if err := os.MkdirAll(cfg.RaftDir, 0700); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}

	snapshots, err := raft.NewFileSnapshotStore(cfg.RaftDir, 2, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.RaftDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("create bolt store: %w", err)
	}

	fsm := &replicatedFSM{state: newFSMState()}

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(string(transport.LocalAddr()))
	raftCfg.SnapshotInterval = 30 * time.Second
	raftCfg.SnapshotThreshold = 2
	raftCfg.ElectionTimeout = 500 * time.Millisecond
	raftCfg.HeartbeatTimeout = 500 * time.Millisecond
	raftCfg.LeaderLeaseTimeout = 300 * time.Millisecond

	r, err := raft.NewRaft(raftCfg, fsm, logStore, logStore, snapshots, transport)
	if err != nil {
		logStore.Close()
		return nil, fmt.Errorf("create raft: %w", err)
	}

	configuration := r.GetConfiguration()
	if configuration.Error() != nil {
		logStore.Close()
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
			// Ignore bootstrap errors
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

	go e.watchLeadership()

	return e, nil
}

func closeCluster(nodes []*testNode) {
	for _, n := range nodes {
		n.elector.Close()
	}
}

// waitForLeader campaigns all nodes and waits for one to become leader.
func waitForLeader(t *testing.T, nodes []*testNode, ctx context.Context) int {
	t.Helper()

	// Campaign on all nodes concurrently.
	for _, n := range nodes {
		go func(e *RaftElector) { _ = e.Campaign(ctx) }(n.elector)
	}

	// Poll for a leader.
	for i := 0; i < 100; i++ {
		for j, n := range nodes {
			if n.elector.IsLeader() {
				return j
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("no leader elected within 20s")
	return -1
}

func TestRaftElector_ImplementsInterface(t *testing.T) {
	var _ Elector = (*RaftElector)(nil)
}

func TestRaftElector_SingleNode_Campaign(t *testing.T) {
	e, cleanup := singleNodeElector(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Campaign(ctx); err != nil {
		t.Fatalf("Campaign: %v", err)
	}

	if !e.IsLeader() {
		t.Error("IsLeader() = false after Campaign, expected true")
	}

	if addr := e.LeaderAddr(); addr != e.cfg.AdvertiseAddr {
		t.Errorf("LeaderAddr() = %q, want %q", addr, e.cfg.AdvertiseAddr)
	}
}

func TestRaftElector_SingleNode_DoneChannel(t *testing.T) {
	e, cleanup := singleNodeElector(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Campaign(ctx); err != nil {
		t.Fatalf("Campaign: %v", err)
	}

	select {
	case <-e.Done():
		t.Error("Done() closed while still leader")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRaftElector_ThreeNodes_ExactlyOneLeader(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Campaign on all nodes concurrently.
	errCh := make([]chan error, len(nodes))
	for i, n := range nodes {
		ch := make(chan error, 1)
		errCh[i] = ch
		go func() { ch <- n.elector.Campaign(ctx) }()
	}

	// Wait for at least one node to become leader.
	leaderIdx := -1
	deadline := time.After(15 * time.Second)
	for leaderIdx == -1 {
		select {
		case <-deadline:
			t.Fatal("no leader elected within 15s")
		default:
		}
		for i, n := range nodes {
			if n.elector.IsLeader() {
				leaderIdx = i
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Wait a bit for cluster stabilization and data replication.
	time.Sleep(2 * time.Second)

	// Exactly one leader.
	leaderCount := 0
	for _, n := range nodes {
		if n.elector.IsLeader() {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Errorf("expected exactly 1 leader, got %d", leaderCount)
	}

	// All nodes should know the leader's address (replicated via FSM).
	// Poll since FSM replication may take a moment.
	addrDeadline := time.After(5 * time.Second)
	addrOK := false
	for !addrOK {
		select {
		case <-addrDeadline:
			t.Fatal("timed out waiting for LeaderAddr to propagate")
		default:
		}
		addrOK = true
		for _, n := range nodes {
			if n.elector.LeaderAddr() == "" {
				addrOK = false
				break
			}
		}
		if !addrOK {
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Cancel context to unblock follower Campaign goroutines.
	cancel()

	// Drain goroutines.
	for _, ch := range errCh {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
		}
	}
}

func TestRaftElector_LeaderShutdown_NewLeaderElected(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaderIdx := waitForLeader(t, nodes, ctx)
	t.Logf("initial leader: node %d", leaderIdx)

	nodes[leaderIdx].elector.Close()

	time.Sleep(5 * time.Second)

	leaderCount := 0
	for i, n := range nodes {
		if i == leaderIdx {
			continue
		}
		if n.elector.IsLeader() {
			leaderCount++
			t.Logf("new leader: node %d", i)
		}
	}

	if leaderCount != 1 {
		t.Errorf("expected 1 new leader after shutdown, got %d", leaderCount)
	}
}

func TestRaftElector_Resign_NewLeaderElected(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaderIdx := waitForLeader(t, nodes, ctx)
	t.Logf("initial leader: node %d", leaderIdx)

	doneCh := nodes[leaderIdx].elector.Done()

	nodes[leaderIdx].elector.Resign()

	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Error("Done() channel did not close after Resign()")
	}

	if nodes[leaderIdx].elector.IsLeader() {
		t.Error("resigned node still reports as leader")
	}

	time.Sleep(5 * time.Second)

	leaderCount := 0
	for i, n := range nodes {
		if n.elector.IsLeader() {
			leaderCount++
			t.Logf("new leader after resign: node %d", i)
		}
	}

	if leaderCount != 1 {
		t.Errorf("expected 1 new leader after resign, got %d", leaderCount)
	}
}

func TestRaftElector_Campaign_ContextCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-cancel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	_, transport := raft.NewInmemTransport(raft.NewInmemAddr())
	peers := []string{string(transport.LocalAddr()), "nonexistent-peer"}

	cfg := RaftConfig{
		Peers:         peers,
		AdvertiseAddr: "http-cancel",
		RaftDir:       dir,
	}

	e, err := newRaftElectorWithTransport(cfg, transport)
	if err != nil {
		t.Fatalf("newRaftElectorWithTransport: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = e.Campaign(ctx)
	if err == nil {
		t.Error("Campaign with cancelled context should return error")
	}
}

func TestRaftElector_InvalidConfig(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		cfg     RaftConfig
		wantErr string
	}{
		{
			name:    "no peers",
			cfg:     RaftConfig{AdvertiseAddr: "localhost:8080", RaftDir: dir},
			wantErr: "raft_peers is required",
		},
		{
			name:    "no advertise addr",
			cfg:     RaftConfig{Peers: []string{"localhost:9000"}, RaftDir: dir},
			wantErr: "advertise_addr is required",
		},
		{
			name:    "no raft dir",
			cfg:     RaftConfig{Peers: []string{"localhost:9000"}, AdvertiseAddr: "localhost:8080"},
			wantErr: "raft_dir is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRaftElector(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tt.wantErr != "" && err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRaftElector_CloseIdempotent(t *testing.T) {
	e, cleanup := singleNodeElector(t)
	defer cleanup()

	if err := e.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRaftElector_ResignWhenNotLeader(t *testing.T) {
	e, cleanup := singleNodeElector(t)
	defer cleanup()

	e.Resign()
}

func TestRaftElector_DoneChannel_FiresOnLeadershipLoss(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaderIdx := waitForLeader(t, nodes, ctx)
	t.Logf("initial leader: node %d", leaderIdx)

	doneCh := nodes[leaderIdx].elector.Done()

	// Close the leader to trigger involuntary leadership loss.
	nodes[leaderIdx].elector.Close()

	// The done channel should close because watchLeadership detects the loss.
	select {
	case <-doneCh:
		// Expected: Done channel closed on involuntary leadership loss.
	case <-time.After(5 * time.Second):
		t.Error("Done() channel did not close after involuntary leadership loss")
	}
}

func singleNodeElector(t *testing.T) (*RaftElector, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "raft-single-*")
	if err != nil {
		t.Fatal(err)
	}

	_, transport := raft.NewInmemTransport(raft.NewInmemAddr())
	peers := []string{string(transport.LocalAddr())}

	cfg := RaftConfig{
		Peers:         peers,
		AdvertiseAddr: "http-single",
		RaftDir:       dir,
	}

	e, err := newRaftElectorWithTransport(cfg, transport)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("NewRaftElector: %v", err)
	}

	cleanup := func() {
		e.Close()
		time.Sleep(100 * time.Millisecond)
		os.RemoveAll(dir)
	}

	return e, cleanup
}

// --- FSM Unit Tests ---

func TestFSM_Apply_SetLeaderAddr(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}
	data, _ := json.Marshal("http-leader-addr")
	cmd := fsmCommand{Op: OpSetLeaderAddr, Data: json.RawMessage(data)}
	b, _ := json.Marshal(cmd)

	l := &raft.Log{Data: b}
	fsm.Apply(l)

	fsm.mu.RLock()
	addr := fsm.state.LeaderAddr
	fsm.mu.RUnlock()

	if addr != "http-leader-addr" {
		t.Errorf("LeaderAddr = %q, want %q", addr, "http-leader-addr")
	}
}

func TestFSM_Apply_SetRunning(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}
	runData, _ := json.Marshal(map[string]string{"issue_id": "ISS-1", "phase": "running"})
	cmd := fsmCommand{Op: OpSetRunning, Key: "ISS-1", Data: json.RawMessage(runData)}
	b, _ := json.Marshal(cmd)

	fsm.Apply(&raft.Log{Data: b})

	fsm.mu.RLock()
	raw, ok := fsm.state.Running["ISS-1"]
	fsm.mu.RUnlock()

	if !ok {
		t.Fatal("Running[ISS-1] not found")
	}
	var m map[string]string
	json.Unmarshal(raw, &m)
	if m["phase"] != "running" {
		t.Errorf("Running[ISS-1].phase = %q, want %q", m["phase"], "running")
	}
}

func TestFSM_Apply_RemoveRunning(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}
	fsm.state.Running["ISS-1"] = json.RawMessage(`{}`)

	cmd := fsmCommand{Op: OpRemoveRunning, Key: "ISS-1"}
	b, _ := json.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: b})

	fsm.mu.RLock()
	_, ok := fsm.state.Running["ISS-1"]
	fsm.mu.RUnlock()

	if ok {
		t.Error("Running[ISS-1] should be removed")
	}
}

func TestFSM_Apply_ClaimReleaseClaim(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}

	// Claim
	cmd := fsmCommand{Op: OpClaim, Key: "ISS-1"}
	b, _ := json.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: b})

	fsm.mu.RLock()
	claimed := fsm.state.Claimed["ISS-1"]
	fsm.mu.RUnlock()
	if !claimed {
		t.Error("ISS-1 should be claimed")
	}

	// Release
	cmd = fsmCommand{Op: OpReleaseClaim, Key: "ISS-1"}
	b, _ = json.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: b})

	fsm.mu.RLock()
	_, exists := fsm.state.Claimed["ISS-1"]
	fsm.mu.RUnlock()
	if exists {
		t.Error("ISS-1 should be released")
	}
}

func TestFSM_Apply_SnapshotState(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}

	newState := newFSMState()
	newState.LeaderAddr = "http-new-leader"
	newState.Running["ISS-1"] = json.RawMessage(`{"phase":"succeeded"}`)
	newState.Claimed["ISS-2"] = true
	data, _ := json.Marshal(newState)

	cmd := fsmCommand{Op: OpSnapshotState, Data: json.RawMessage(data)}
	b, _ := json.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: b})

	fsm.mu.RLock()
	defer fsm.mu.RUnlock()

	if fsm.state.LeaderAddr != "http-new-leader" {
		t.Errorf("LeaderAddr = %q, want %q", fsm.state.LeaderAddr, "http-new-leader")
	}
	if len(fsm.state.Running) != 1 {
		t.Errorf("Running len = %d, want 1", len(fsm.state.Running))
	}
	if !fsm.state.Claimed["ISS-2"] {
		t.Error("ISS-2 should be claimed")
	}
}

func TestFSM_BackwardCompat_OldLeaderData(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}

	// Old-format: just {"advertise_addr": "http-old"}
	oldData, _ := json.Marshal(map[string]string{"advertise_addr": "http-old"})
	fsm.Apply(&raft.Log{Data: oldData})

	fsm.mu.RLock()
	addr := fsm.state.LeaderAddr
	fsm.mu.RUnlock()

	if addr != "http-old" {
		t.Errorf("LeaderAddr = %q, want %q (backward compat)", addr, "http-old")
	}
}

func TestFSM_SnapshotRestore(t *testing.T) {
	fsm := &replicatedFSM{state: newFSMState()}
	fsm.state.LeaderAddr = "http-leader"
	fsm.state.Running["ISS-1"] = json.RawMessage(`{"phase":"running"}`)
	fsm.state.Claimed["ISS-2"] = true

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Persist to buffer.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		sink := &testSink{Writer: pw}
		snap.Persist(sink)
	}()

	// Restore into fresh FSM.
	fsm2 := &replicatedFSM{state: newFSMState()}
	if err := fsm2.Restore(pr); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	fsm2.mu.RLock()
	defer fsm2.mu.RUnlock()

	if fsm2.state.LeaderAddr != "http-leader" {
		t.Errorf("LeaderAddr = %q, want %q", fsm2.state.LeaderAddr, "http-leader")
	}
	if len(fsm2.state.Running) != 1 {
		t.Errorf("Running len = %d, want 1", len(fsm2.state.Running))
	}
	if !fsm2.state.Claimed["ISS-2"] {
		t.Error("ISS-2 should be claimed after restore")
	}
}

// testSink implements raft.SnapshotSink for testing.
type testSink struct {
	io.Writer
}

func (t *testSink) Close() error       { return nil }
func (t *testSink) ID() string         { return "test" }
func (t *testSink) Cancel() error      { return nil }

func TestRaftElector_ApplyCommand_ReplicatesToFollowers(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaderIdx := waitForLeader(t, nodes, ctx)
	leader := nodes[leaderIdx].elector

	// Apply a command on the leader.
	runData, _ := json.Marshal(map[string]string{"issue_id": "ISS-1", "phase": "running"})
	if err := leader.ApplyCommand(OpSetRunning, "ISS-1", runData); err != nil {
		t.Fatalf("ApplyCommand: %v", err)
	}

	// Wait for replication.
	time.Sleep(2 * time.Second)

	// Check that all nodes have the data in their FSM.
	for i, n := range nodes {
		data, err := n.elector.ReplicatedState()
		if err != nil {
			t.Errorf("ReplicatedState node %d: %v", i, err)
			continue
		}
		var state fsmState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Errorf("unmarshal state node %d: %v", i, err)
			continue
		}
		raw, ok := state.Running["ISS-1"]
		if !ok {
			t.Errorf("node %d: Running[ISS-1] not found", i)
			continue
		}
		var m map[string]string
		json.Unmarshal(raw, &m)
		if m["phase"] != "running" {
			t.Errorf("node %d: phase = %q, want %q", i, m["phase"], "running")
		}
	}
}

func TestRaftElector_ApplyCommand_NotLeader(t *testing.T) {
	nodes := createCluster(t, 3)
	defer closeCluster(nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaderIdx := waitForLeader(t, nodes, ctx)

	// Find a follower.
	followerIdx := (leaderIdx + 1) % 3
	follower := nodes[followerIdx].elector

	// Apply on follower should fail.
	err := follower.ApplyCommand(OpSetRunning, "ISS-1", []byte(`{}`))
	if err == nil {
		t.Error("ApplyCommand on follower should return error")
	}
}

func TestRaftElector_ReplicatedState(t *testing.T) {
	e, cleanup := singleNodeElector(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Campaign(ctx); err != nil {
		t.Fatalf("Campaign: %v", err)
	}

	// Apply some data.
	runData, _ := json.Marshal(map[string]string{"issue_id": "ISS-1"})
	e.ApplyCommand(OpSetRunning, "ISS-1", runData)
	e.ApplyCommand(OpClaim, "ISS-2", nil)

	data, err := e.ReplicatedState()
	if err != nil {
		t.Fatalf("ReplicatedState: %v", err)
	}

	var state fsmState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if state.LeaderAddr != "http-single" {
		t.Errorf("LeaderAddr = %q, want %q", state.LeaderAddr, "http-single")
	}
	if len(state.Running) != 1 {
		t.Errorf("Running len = %d, want 1", len(state.Running))
	}
	if !state.Claimed["ISS-2"] {
		t.Error("ISS-2 should be claimed")
	}
}
