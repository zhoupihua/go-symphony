# Task List: Embedded Raft Elector

## Task 1: Add hashicorp/raft dependency
- [ ] `go get github.com/hashicorp/raft`
- [ ] `go get github.com/hashicorp/raft-boltdb/v2`
- [ ] Verify build passes
- Acceptance: `go build ./...` succeeds with new deps

## Task 2: Update HAConfig schema
- [ ] Remove `EtcdEndpoints`, `LeaseTTLMS` from HAConfig
- [ ] Add `RaftPeers []string`, `RaftDir string`
- [ ] Update `applyDefaults`: RaftDir defaults to `{workspace.root}/raft`
- [ ] Update `Validate`: RaftPeers required when ha.enabled=true, AdvertiseAddr required
- [ ] Update config_test.go
- Acceptance: `go test ./internal/config/` passes

## Task 3: Implement RaftElector
- [ ] Create `internal/ha/raft.go` with RaftElector struct
- [ ] Implement minimal FSM (stores leader identity only)
- [ ] Implement Campaign: bootstrap Raft, wait for leadership via LeaderCh()
- [ ] Implement IsLeader: check raft.State == Leader
- [ ] Implement Resign: leadership transfer
- [ ] Implement Done: channel from LeaderCh() observation goroutine
- [ ] Implement LeaderAddr: read from FSM
- [ ] Support single-node mode (raft_peers = [self])
- [ ] Implement config validation in NewRaftElector
- Acceptance: `go test ./internal/ha/` passes (unit tests)

## Task 4: Remove EtcdElector and etcd dependency
- [ ] Delete `internal/ha/etcd.go`
- [ ] Delete `internal/ha/etcd_test.go`
- [ ] Update `main.go`: replace EtcdElector with RaftElector
- [ ] Remove etcd-related imports from main.go
- [ ] `go mod tidy` to remove etcd deps
- [ ] Verify no `go.etcd.io` references remain
- Acceptance: `go build ./...` and `go test ./...` pass without etcd

## Task 5: Integration tests for RaftElector
- [ ] Test single-node Campaign returns quickly
- [ ] Test 3-node cluster: exactly 1 leader elected
- [ ] Test LeaderAddr on follower returns leader's advertise addr
- [ ] Test leader shutdown → new leader elected
- [ ] Test Resign → new leader elected
- [ ] Test Done channel closes on leadership loss
- Acceptance: all integration tests pass with real Raft transport (inmem)
