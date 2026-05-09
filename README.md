# Symphony Go

Go implementation of the Symphony orchestration service. Symphony is a long-running automation daemon that polls issue trackers for work, creates isolated workspaces per issue, and runs coding agents inside those workspaces.

The authoritative service specification is [SPEC.md](reference/symphony/SPEC.md). This implementation conforms to it with Go-specific extensions documented in [SPEC-GO.md](SPEC-GO.md).

## Architecture

Six abstraction layers, each mapping to an internal package:

| Layer | Package | Responsibility |
|-------|---------|---------------|
| Policy | `workflow`, `config` | WORKFLOW.md loading, YAML front matter parsing, typed config, prompt template |
| Configuration | `config` | Struct with YAML tags, defaults, `$VAR` resolution, validation |
| Coordination | `orchestrator` | Poll loop, dispatch, reconcile, retry with exponential backoff |
| Execution | `workspace`, `agentrunner` | Workspace lifecycle, agent subprocess management, hook execution |
| Integration | `tracker` (interface), `linear`, `plane` | Issue tracker API adapters |
| Observability | `httpserver`, `ha` | Dashboard, SSE, health endpoint, leader election |

Core interfaces:

- `tracker.Tracker` -- FetchCandidateIssues, FetchIssuesByStates, FetchIssueStatesByIDs, CreateComment, UpdateIssueState
- `agent.Agent` / `agent.Session` -- StartSession, RunTurn, Close
- `ha.Elector` -- Campaign, IsLeader, Resign, Done, LeaderAddr

Adapters are selected by WORKFLOW.md config (`tracker.kind`, `agent.kind`) and registered at startup via `RegisterTracker` / `RegisterAgent`.

## Quick Start

```bash
go build ./cmd/symphony
./symphony -config WORKFLOW.md
```

Minimal WORKFLOW.md:

```yaml
---
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  linear:
    project_slug: my-project
agent:
  kind: codex
---

You are an AI coding assistant working on the following issue:
{{.Issue.Identifier}}: {{.Issue.Title}}

{{.Issue.Description}}
```

## Configuration Reference

All runtime behavior is configured through WORKFLOW.md YAML front matter. The prompt body (everything after the second `---`) is a Go `text/template` with `.Issue` and `.Attempt` variables.

### Top-Level Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `tracker` | object | -- | Tracker adapter selection and credentials |
| `polling` | object | -- | Poll interval settings |
| `workspace` | object | -- | Workspace root path |
| `hooks` | object | -- | Workspace lifecycle hooks |
| `agent` | object | -- | Agent adapter and concurrency settings |
| `worker` | object | -- | SSH remote execution |
| `ha` | object | -- | High-availability leader election |
| `server` | object | -- | HTTP server settings |

### tracker

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kind` | string | -- | Required. `"linear"` or `"plane"` |
| `api_key` | string | -- | Required. Literal token or `$VAR_NAME` |
| `active_states` | []string | `["Todo", "In Progress"]` | States eligible for dispatch |
| `terminal_states` | []string | `["Closed", "Cancelled", "Canceled", "Duplicate", "Done"]` | States triggering workspace cleanup |
| `linear.project_slug` | string | -- | Required when kind=linear |
| `linear.endpoint` | string | `https://api.linear.app/graphql` | Linear API endpoint |
| `plane.workspace_slug` | string | -- | Required when kind=plane |
| `plane.project_id` | string | -- | Required when kind=plane |
| `plane.endpoint` | string | `https://api.plane.so/api/` | Plane API endpoint |

### polling

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `interval_ms` | int | `30000` | Milliseconds between poll cycles |

### workspace

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `root` | string | `<tmp>/symphony_workspaces` | Absolute path for workspace directories. Supports `~` and `$VAR` expansion. |

### hooks

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `after_create` | string | -- | Shell script run when workspace is newly created. Failure aborts. |
| `before_run` | string | -- | Shell script run before each agent attempt. Failure aborts. |
| `after_run` | string | -- | Shell script run after each agent attempt. Failure logged, ignored. |
| `before_remove` | string | -- | Shell script run before workspace removal. Failure logged, ignored. |
| `timeout_ms` | int | `60000` | Timeout for all hook executions |

### agent

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kind` | string | -- | Required. `"codex"` or `"claude"` |
| `max_concurrent` | int | `10` | Maximum concurrent agent sessions |
| `max_turns` | int | `20` | Maximum turns per agent session |
| `max_retry_backoff_ms` | int | `300000` | Retry backoff cap (5 minutes) |
| `max_concurrent_by_state` | map | `{}` | Per-state concurrency overrides |

### agent.codex

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `command` | string | `codex app-server` | Shell command to launch Codex |
| `approval_policy` | string | `auto` | Approval policy for Codex actions |
| `thread_sandbox` | string | -- | Codex sandbox mode |
| `turn_sandbox_policy` | string | -- | Codex turn sandbox policy |
| `turn_timeout_ms` | int | `300000` | Turn stream timeout |
| `read_timeout_ms` | int | `30000` | Request/response read timeout |
| `stall_timeout_ms` | int | `300000` | Inactivity timeout (0 disables) |

### agent.claude

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `command` | string | `claude` | Path to Claude Code CLI |
| `permission_mode` | string | -- | Permission mode (e.g., `"auto"`) |
| `allowed_tools` | []string | -- | Tools to allow when not in auto mode |
| `max_turns` | int | `10` | Max turns per Claude session |

### worker

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ssh_hosts` | []string | -- | SSH host strings for remote execution |

### ha

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable HA leader election |
| `etcd_endpoints` | []string | `["localhost:2379"]` | etcd cluster endpoints |
| `lease_ttl_ms` | int | `5000` | Leader lease TTL in milliseconds |
| `advertise_addr` | string | -- | Address this instance advertises as leader |

### server

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | `8080` | HTTP listen port |
| `host` | string | `localhost` | HTTP listen host |

## Supported Trackers

### Linear

GraphQL-based adapter. Queries issues by project slug and active states. Supports pagination, blocker resolution from inverse relations, and label normalization.

```yaml
tracker:
  kind: linear
  api_key: $LINEAR_API_KEY
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Cancelled"]
  linear:
    project_slug: my-team/my-project
```

### Plane

REST-based adapter. Queries issues by workspace slug and project ID.

```yaml
tracker:
  kind: plane
  api_key: $PLANE_API_KEY
  plane:
    workspace_slug: my-workspace
    project_id: abc123-def456
```

## Supported Agents

### Codex

JSON-RPC 2.0 over stdio. Persistent subprocess: one `codex app-server` process per issue, with multiple turns over the same session. Supports approval handling, dynamic tools (`linear_graphql`, `plane_rest`), and configurable timeouts.

```yaml
agent:
  kind: codex
  codex:
    command: codex app-server
    approval_policy: auto
    turn_timeout_ms: 600000
```

### Claude Code

NDJSON stream over stdout. Per-turn subprocess: each `claude -p <prompt> --output-format stream-json` invocation is a separate process. No persistent session between turns.

```yaml
agent:
  kind: claude
  claude:
    command: claude
    permission_mode: auto
    allowed_tools: ["Bash", "Read", "Write"]
    max_turns: 10
```

## Deployment Modes

| Mode | ha.enabled | worker.ssh_hosts | etcd | Description |
|------|-----------|-----------------|------|-------------|
| Local single | false | empty | no | Single instance, local execution |
| Cloud single | false | empty | no | Single instance, no SSH |
| Cloud + SSH workers | false | configured | no | Central orchestrator, remote agents |
| Cloud HA | true | empty | yes | etcd leader election, no SSH |
| Cloud HA + SSH | true | configured | yes | Full HA with remote workers |

SSH remote execution uses key-based authentication via `golang.org/x/crypto/ssh`. Workspace operations and agent commands run over SSH sessions. Codex uses persistent SSH sessions; Claude Code uses per-turn SSH commands.

## CLI Flags

```
Usage: symphony [flags]

Flags:
  -config string    Path to WORKFLOW.md config file (default "WORKFLOW.md")
  -addr string      HTTP listen address (overrides config)
  -version          Print version and exit
  -log-level string Log level: debug, info, warn, error (default "info")
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check, returns `ok` |
| GET | `/api/v1/state` | Current orchestrator state as JSON |
| POST | `/api/v1/refresh` | Trigger immediate poll cycle |
| GET | `/api/v1/events` | Server-Sent Events stream of state updates |

### State Response

```json
{
  "leader": true,
  "running_count": 2,
  "running": {
    "issue-id": {
      "issue_id": "abc123",
      "identifier": "ENG-42",
      "title": "Fix auth bug",
      "state": "In Progress",
      "labels": ["bug"],
      "url": "https://linear.app/...",
      "worker_host": "",
      "workspace_path": "/tmp/symphony_workspaces/ENG-42",
      "attempt": 1,
      "started_at": "2026-01-15T10:30:00Z",
      "last_activity": "2026-01-15T10:35:00Z",
      "turn_count": 3,
      "input_tokens": 5000,
      "output_tokens": 2000,
      "total_tokens": 7000
    }
  }
}
```

## Dashboard

Web UI served at the root path using `html/template` + htmx + SSE. Real-time updates via the `/api/v1/events` SSE endpoint. No JavaScript framework required.

- Leader instances serve the dashboard with live orchestrator state.
- Standby instances redirect to the leader's dashboard.

## Building and Testing

```bash
# Build
go build ./cmd/symphony

# Build all packages
go build ./...

# Run tests with race detector
go test -timeout 120s -race ./...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Vet
go vet ./...

# Format check (list files needing formatting)
gofmt -l .

# Format in place
gofmt -w .

# Run with default config
go run ./cmd/symphony -config WORKFLOW.md
```

## Hot Reload

WORKFLOW.md is watched for changes. When the file is modified:

1. The file is re-read and parsed.
2. Config and prompt template are re-applied without restart.
3. Future poll cycles, dispatch decisions, and agent launches use the new config.
4. In-flight sessions are not interrupted.
5. Invalid reloads keep the last known good config and log an error.

## Module

```
github.com/zhoupihua/go-symphony
```

Requires Go 1.26.1 or later.
