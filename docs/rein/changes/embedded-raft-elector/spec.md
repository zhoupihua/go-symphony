# Spec: Embedded Raft Elector

## Requirements

### R1: RaftElector implements Elector interface

**WHEN** `NewRaftElector(cfg)` is called with valid config
**THEN** the returned value satisfies `ha.Elector` interface
**TEST** compile-time check + runtime type assertion

### R2: Campaign acquires leadership

**WHEN** `RaftElector.Campaign(ctx)` is called on a node
**THEN** it blocks until this node becomes the Raft leader (or context cancelled)
**AND** `IsLeader()` returns `true` after successful campaign
**TEST** start 3-node inmem Raft cluster, verify exactly 1 leader

### R3: IsLeader reflects actual Raft state

**WHEN** Raft leadership changes (e.g., leader process killed)
**THEN** the former leader's `IsLeader()` returns `false`
**AND** a new leader's `IsLeader()` returns `true`
**TEST** shutdown leader, verify new leader elected within 2x election timeout

### R4: Done channel signals leadership loss

**WHEN** the current leader loses Raft leadership
**THEN** the `Done()` channel is closed
**TEST** force leadership transfer, verify Done channel closes

### R5: LeaderAddr returns leader's advertise address

**WHEN** `LeaderAddr()` is called on any node (leader or follower)
**THEN** it returns the `advertise_addr` of the current Raft leader
**TEST** 3-node cluster, call LeaderAddr on follower, matches leader's config

### R6: Resign triggers leadership transfer

**WHEN** `Resign()` is called on the leader
**THEN** the node steps down as Raft leader
**AND** a new leader is elected among remaining peers
**TEST** leader resigns, verify new leader within 2x election timeout

### R7: Auto re-campaign after leadership loss

**WHEN** leadership is lost (network partition, etc.)
**THEN** the node automatically re-campaigns without process restart
**TEST** simulate leadership loss, verify node re-acquires leadership

### R8: Configuration validation

**WHEN** `NewRaftElector(cfg)` is called with:
- empty `RaftPeers` → returns error
- empty `AdvertiseAddr` → returns error
- empty `RaftDir` → uses default `{workspace.root}/raft`
**TEST** table-driven validation tests

### R9: Single-node Raft mode

**WHEN** `RaftPeers` contains only one address (this node)
**THEN** RaftElector operates in single-node mode for dev/test
**AND** Campaign returns immediately
**TEST** single-node config, Campaign returns in < 100ms

### R10: Remove etcd dependency

**WHEN** the RaftElector implementation is complete
**THEN** no `go.etcd.io/etcd/*` imports exist in the codebase
**AND** `go mod tidy` removes etcd from go.sum
**TEST** `grep -r "go.etcd.io" go/` returns empty

### R11: Config schema update

**WHEN** WORKFLOW.md contains `ha.raft_peers`
**THEN** `config.Schema.HA` contains `RaftPeers []string` and `RaftDir string`
**AND** `ha.etcd_endpoints` and `ha.lease_ttl_ms` are removed
**TEST** parse WORKFLOW.md with new ha config, verify fields populated

### R12: Backward compatibility of Elector interface

**WHEN** the RaftElector is used by orchestrator and httpserver
**THEN** no changes are needed in orchestrator.go or server.go
**AND** all existing orchestrator and httpserver tests pass unchanged
**TEST** `go test ./internal/orchestrator/ ./internal/httpserver/` passes

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Raft library | hashicorp/raft | Proven in Consul/Nomad/Vault, pure Go, well-maintained |
| Log storage | hashicorp/raft-boltdb | BoltDB is embedded, zero-config, proven with hashicorp/raft |
| Transport | hashicorp/raft TCP transport | Standard, no custom protocol needed |
| FSM | Minimal (leader identity only) | We only need leader election, not state replication |
| Re-campaign | Auto via LeaderCh() | Raft natively supports leadership observation |
| Single-node mode | Supported | Essential for dev/test, Raft handles this natively |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| hashicorp/raft API 复杂，实现出错 | Medium | High | 参照 Consul/Nomad 的 Raft 封装，写充足的集成测试 |
| Raft 选举延迟比 etcd 长 | Low | Medium | 调整 ElectionTimeout（默认 1s），足够快 |
| BoltDB 文件损坏 | Low | High | 启动时检测并报错，文档说明恢复步骤（删 raft dir 重新加入） |
| 静态 peer 配置不够灵活 | Medium | Low | 本期 Non-Goal，未来可通过 API 或配置热更新支持 |
