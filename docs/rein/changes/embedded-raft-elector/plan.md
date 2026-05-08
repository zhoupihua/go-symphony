# Plan: Embedded Raft Elector

## Architecture Overview

```
main.go
  ├── ha.NewRaftElector(cfg)   ← 新实现
  ├── ha.NewLocalElector()     ← 不变
  └── ha.Elector interface     ← 不变

internal/ha/
  ├── elector.go      ← 不变
  ├── local.go        ← 不变
  ├── local_test.go   ← 不变
  ├── raft.go         ← 新增：RaftElector
  └── raft_test.go    ← 新增：RaftElector 测试（含多节点集成测试）

删除:
  ├── etcd.go
  └── etcd_test.go

internal/config/
  └── schema.go       ← HAConfig 字段变更
```

## Task Breakdown

### Task 1: Add hashicorp/raft dependency
- `go get github.com/hashicorp/raft` + `github.com/hashicorp/raft-boltdb/v2`
- 验证编译通过

### Task 2: Update HAConfig schema
- 移除 `EtcdEndpoints []string` 和 `LeaseTTLMS int`
- 添加 `RaftPeers []string` 和 `RaftDir string`
- 更新 `config.go` 的 applyDefaults 和 Validate
- 更新 config_test.go

### Task 3: Implement RaftElector
- `raft.go`: 实现 `ha.Elector` 接口
  - FSM: 极简状态机，仅存储 leader identity
  - Campaign: 启动 Raft，等待成为 leader
  - IsLeader: 检查 Raft.State
  - Resign: leadership transfer
  - Done: 基于 LeaderCh() 通道
  - LeaderAddr: 从 FSM 读取
  - 单节点模式支持
- 配置验证

### Task 4: Remove EtcdElector and etcd dependency
- 删除 `internal/ha/etcd.go` 和 `internal/ha/etcd_test.go`
- 更新 `main.go`：用 RaftElector 替换 EtcdElector
- `go mod tidy` 移除 etcd 依赖

### Task 5: Integration tests for RaftElector
- 单节点模式测试
- 3 节点集群测试（inmem transport）
- Leader 丢失 + 重新选举测试
- Resign + 新 leader 选举测试

### Dependency Graph
```
Task 1 → Task 3 (需要 raft 库)
Task 2 → Task 3 (需要新配置)
Task 3 → Task 4 (需要 RaftElector 先可用)
Task 3 → Task 5 (需要实现后测试)
```

## Checkpoints

- [ ] Task 1-2: 依赖就位，配置 schema 更新，编译通过
- [ ] Task 3: RaftElector 实现完成，单元测试通过
- [ ] Task 4: etcd 完全移除，编译通过
- [ ] Task 5: 集成测试全部通过
- [ ] 全量 `go test ./...` 通过
