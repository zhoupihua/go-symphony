# Proposal: Replace etcd with Embedded Raft for Leader Election

## Why

当前 HA 方案使用外部 etcd 集群做选主，但 Symphony 只需要 leader election 这一个能力，不需要 KV store、watch、lease 等 etcd 的其他特性。这导致：

1. **运维负担重**：部署 Symphony HA 需要先搭一套 etcd 集群（至少 3 节点），运维成本甚至超过 Symphony 本身
2. **依赖膨胀**：etcd client 引入 gRPC、protobuf、zap 等大量传递依赖，go.sum 新增 200+ 行
3. **选主逻辑与业务逻辑割裂**：etcd 是外部服务，选主失败时的重试、re-campaign 等逻辑散落在 main.go 和 etcd.go 之间，没有形成闭环
4. **测试盲区**：EtcdElector 的核心选主逻辑完全没有集成测试（需要真实 etcd），导致 HA 路径基本不可测

**核心判断**：etcd 是分布式 KV 存储，不是选主库。用它只做选主，是大炮打蚊子。

## What Changes

用 **hashicorp/raft** 替换 etcd，将 Raft 共识直接嵌入 Symphony 进程。Symphony 集群成员本身就是 Raft peer，无需外部服务。

### 新增：`RaftElector`

实现 `ha.Elector` 接口，内部使用 `hashicorp/raft`：

- **Transport**：TCP，Raft peer 间直接通信（复用 `worker.ssh_hosts` 配置模式，新增 `ha.raft_peers`）
- **FSM**：极简状态机，仅存储 leader identity（who is leader + advertise addr），不做 KV 复制
- **Persistence**：BoltDB 存储 Raft log + snapshot（`hashicorp/raft-boltdb`）
- **Leader 选举**：Raft 协议原生提供，不需要额外实现
- **Leader 转换**：通过 Raft 的 `LeaderCh()` 通道实时感知

### 移除：`EtcdElector`

- 删除 `internal/ha/etcd.go`
- 从 `go.mod` 移除所有 `go.etcd.io/etcd/*` 依赖
- 从 `config.Schema` 移除 `ha.etcd_endpoints`，替换为 `ha.raft_peers`

### 配置变更

```yaml
# 旧配置
ha:
  enabled: true
  etcd_endpoints: ["etcd1:2379", "etcd2:2379", "etcd3:2379"]
  lease_ttl_ms: 10000
  advertise_addr: "10.0.0.5:8080"

# 新配置
ha:
  enabled: true
  raft_peers: ["10.0.0.5:9300", "10.0.0.6:9300", "10.0.0.7:9300"]
  raft_dir: "/var/lib/symphony/raft"   # Raft log 持久化目录
  advertise_addr: "10.0.0.5:8080"
```

## Goals

1. 消除 etcd 外部依赖，Symphony 自身即可组成 HA 集群
2. 保持 `Elector` 接口不变，对 orchestrator 和 httpserver 零改动
3. 补全 HA 路径的测试覆盖（RaftElector 可在单进程内用 inmem transport 测试）
4. 实现 re-campaign 逻辑：leader 丢失后自动重新竞选，无需重启进程

## Non-Goals

1. **不做状态复制**：不将 orchestrator 的 running/retry 状态通过 Raft 复制到 follower。这是未来扩展，不在本次范围
2. **不做动态成员变更**：初始部署时静态配置 Raft peers，不支持运行时增减节点
3. **不兼容旧配置**：`ha.etcd_endpoints` 直接替换为 `ha.raft_peers`，不做向后兼容迁移
4. **不保留 EtcdElector 代码**：完全移除，不做双实现共存

## Assumptions

1. Symphony HA 集群规模小（3-5 节点），不需要 etcd 级别的水平扩展能力
2. 所有 Raft peer 网络可达，无 NAT 穿透需求
3. `hashicorp/raft` 的成熟度足以支撑生产环境（Consul、Nomad、Vault 均使用该库）
4. BoltDB 作为嵌入式存储，性能足以满足 3-5 节点 Raft 的 log 持久化需求

## Open Questions

1. **Raft 数据目录**：默认值应放在哪里？`/var/lib/symphony/raft` 还是 `{workspace.root}/raft`？
2. **Raft 端口**：是否需要独立配置 Raft bind 端口，还是自动 derive from `advertise_addr`？
3. **单节点 Raft**：当 `raft_peers` 只有一个地址时，是否允许单节点 Raft 模式（用于开发/测试）？
4. **快照策略**：FSM 极简情况下，是否需要定期快照？还是 log truncation 即可？
