# Symphony Go Implementation Specification

This document captures Go-specific extensions and implementation details beyond the language-agnostic [SPEC.md](reference/symphony/SPEC.md). When this document conflicts with SPEC.md on Go-specific behavior, this document takes precedence. For all other behavior, SPEC.md is the authority.

## Module

```
github.com/zhoupihua/go-symphony
```

Go 1.26.1 or later.

## Adapter Registry Pattern

The Go implementation uses an explicit registry instead of Elixir's module-based dispatch. Adapters register factory functions at program startup.

```go
// Registration (in main.go)
tracker.RegisterTracker("linear", linear.NewAdapter)
tracker.RegisterTracker("plane", plane.NewAdapter)
agent.RegisterAgent("codex", codex.NewAdapter)
agent.RegisterAgent("claude", claude.NewAdapter)

// Lookup (in orchestrator)
trk, err := tracker.NewTracker(cfg.Tracker.Kind, trackerConfigMap(cfg))
ag, err := agent.NewAgent(cfg.Agent.Kind, agentConfigFromSchema(cfg))
```

Factories receive a `map[string]any` of adapter-specific config and return an interface instance. Duplicate registrations panic at startup.

## Codex Adapter

Protocol: JSON-RPC 2.0 over stdio with a persistent subprocess per issue.

Session lifecycle:

1. Spawn `bash -lc <codex.command>` in the workspace directory.
2. Send `initialize` request, wait for response.
3. Send `initialized` notification.
4. Send `thread/start` request with approval policy, sandbox, cwd, and dynamic tools.
5. Extract `threadId` from the response.
6. For each turn: send `turn/start`, process the message loop until `turn/completed`, `turn/failed`, or `turn/cancelled`.

Message handling during a turn:

| Server Message | Action |
|---------------|--------|
| `turn/completed` | Return TurnResult with usage and output |
| `turn/failed` | Return error |
| `turn/cancelled` | Return error |
| `item/command_approval` | Auto-approve or reject based on `approval_policy` |
| `item/file_change_approval` | Auto-approve or reject based on `approval_policy` |
| `exec/command_approval` | Auto-approve or reject based on `approval_policy` |
| `apply_patch_approval` | Auto-approve or reject based on `approval_policy` |
| `item/tool_request_user_input` | Fail the turn (not supported in automated mode) |
| `item/tool_call` | Execute dynamic tool (`linear_graphql`, `plane_rest`) |

Dynamic tools advertised at session start:

- `linear_graphql` -- Execute GraphQL queries against the Linear API using configured tracker auth.
- `plane_rest` -- Make REST API calls to Plane using configured tracker auth.

Unsupported tool calls return a failure response without stalling the session.

Timeouts:

- `read_timeout_ms` -- Per-read timeout for JSON-RPC responses (default 5s).
- `turn_timeout_ms` -- Total turn stream timeout (default 1h).
- `stall_timeout_ms` -- Inactivity timeout enforced by the orchestrator (default 5m, 0 disables).

## Claude Code Adapter

Protocol: NDJSON stream over stdout, one subprocess per turn.

Session lifecycle:

1. `StartSession` returns a `ClaudeSession` struct (no subprocess yet).
2. Each `RunTurn` spawns: `claude -p <prompt> --output-format stream-json [--dangerously-skip-permissions] [--allowedTools ...] [--max-turns N]`
3. Parse NDJSON events: `assistant` (text), `result` (final), `usage` (token counts).
4. Process exits after each turn; no persistent subprocess.

Key differences from Codex:

- No persistent session between turns. Each turn is an independent process invocation.
- No JSON-RPC handshake. The CLI handles session management internally.
- Permission mode maps: `"auto"`, `"bypassPermissions"`, `"dangerously-skip-permissions"` all map to `--dangerously-skip-permissions`.
- Token accounting from `result` and `usage` NDJSON events.

## SSH Remote Execution

The `sshclient` package provides SSH operations using `golang.org/x/crypto/ssh`.

| Operation | Method | Description |
|-----------|--------|-------------|
| Connect | `Dial(ctx)` | Key-based auth, configurable known_hosts |
| Run command | `RunCommand(ctx, client, cmd, dir)` | Prefixes `cd <dir> &&` if dir is set |
| Create directory | `MkdirAll(ctx, client, dir)` | `mkdir -p` on remote host |
| Remove directory | `RemoveAll(ctx, client, dir)` | `rm -rf` on remote host |
| Copy file | `CopyFile(ctx, client, localPath, remoteDir)` | `cat >` over SSH session |

SSH config:

- Default port: 22
- Default connect timeout: 30s
- Auth: public key only (no password auth)
- Host key verification: configurable known_hosts file; skips if not configured (with warning)

Execution model differences by agent:

| Agent | SSH Mode | Description |
|-------|----------|-------------|
| Codex | Persistent session | SSH session maintained across multiple turns |
| Claude Code | Per-turn command | Each turn spawns a new SSH command execution |

## High Availability

### LocalElector

Single-instance elector for non-HA deployments. Always returns `IsLeader() == true`. No external dependencies. Used when `ha.enabled` is false.

```go
elector := ha.NewLocalElector()
// Campaign() returns immediately
// IsLeader() always returns true
// Resign() is a no-op
```

### RaftElector

Embedded hashicorp/raft leader election with BoltDB storage. Used when `ha.enabled` is true.

Behavior:

- `Campaign(ctx)` blocks until leadership is acquired or context is cancelled.
- A background goroutine watches the Raft `LeaderCh()` channel; if leadership is lost, the `Done()` channel is closed.
- `LeaderAddr()` returns the current Raft leader's advertised address.
- `Resign()` transfers leadership to another node via `LeadershipTransfer()`.
- Raft configuration: `ha.raft_peers`, `ha.raft_dir` (BoltDB data directory), `ha.advertise_addr`.

FSM state replication:

- State mutations (running, claimed, retries, completed) are replicated across the Raft cluster via `ApplyCommand`.
- FSM supports snapshot/restore for log compaction.
- Cluster management: `AddVoter`, `RemoveServer`, `GetConfiguration`.

Orchestrator behavior under HA:

- The orchestrator poll loop checks `elector.Done()` and `IsLeader()` before each tick.
- Only the leader instance dispatches work and runs reconciliation.
- Standby instances skip poll ticks but remain ready to campaign if the leader fails.

## HTTP Server

The `httpserver` package provides an HTTP server with JSON API and SSE streaming.

Endpoints:

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/healthz` | `handleHealthz` | Returns `ok` (plain text) |
| GET | `/api/v1/state` | `handleState` | JSON snapshot of orchestrator state |
| POST | `/api/v1/refresh` | `handleRefresh` | Trigger immediate poll cycle |
| GET | `/api/v1/events` | `handleEvents` | SSE stream with state updates every 3s |

CORS headers are set on all `/api/v1/*` endpoints (`Access-Control-Allow-Origin: *`).

Server defaults:

- Bind host: `0.0.0.0` (overridden by `server.host`)
- Default port: 8080 (overridden by `server.port`)
- Read header timeout: 5s
- Read timeout: 10s
- Write timeout: 30s
- Idle timeout: 60s

The `-addr` CLI flag overrides `server.host` and `server.port` when set.

## Dashboard

Server-rendered HTML using `html/template` with htmx for dynamic updates and SSE for real-time state streaming.

- No JavaScript framework dependency.
- Dashboard renders from the `StateProvider` interface (`Running()`, `RunningCount()`).
- Leader instances serve the dashboard directly.
- Standby instances redirect to the leader's dashboard (using `LeaderAddr()` from the elector).

## Prompt Rendering

Uses Go's `text/template` with strict mode (`missingkey=error`). Unknown variables cause an error.

Available template data:

| Variable | Type | Description |
|----------|------|-------------|
| `.Issue` | `tracker.Issue` | Normalized issue object |
| `.Issue.ID` | string | Tracker-internal ID |
| `.Issue.Identifier` | string | Human-readable key (e.g. `ENG-42`) |
| `.Issue.Title` | string | Issue title |
| `.Issue.Description` | string | Issue body |
| `.Issue.State` | string | Current tracker state |
| `.Issue.Priority` | *int | Priority (nil if unset) |
| `.Issue.Labels` | []string | Normalized labels |
| `.Issue.URL` | string | Tracker URL |
| `.Issue.BlockedBy` | []string | Blocking issue identifiers |
| `.Attempt` | int | Attempt number (0 for first run) |

Custom template functions:

| Function | Signature | Description |
|----------|-----------|-------------|
| `join` | `strings.Join` | Join a slice of strings with a separator |

## Differences from Elixir Reference

| Aspect | Elixir | Go |
|--------|--------|-----|
| Concurrency model | OTP supervision trees, process mailboxes | `context.Context` for cancellation, goroutines and channels |
| Adapter dispatch | Module-based dispatch (`Code.ensure_loaded`) | Explicit registry (`RegisterTracker`/`RegisterAgent`) with factory functions |
| Subprocess management | `Port.open` for stdio | `os/exec.Cmd` with `StdinPipe`/`StdoutPipe` |
| Config hot-reload | `FileSystemWatcher` + ETS | Polling file watcher (1s interval) with `sync.RWMutex`-protected store |
| State management | GenServer state, process dictionaries | `sync.RWMutex`-protected `State` struct with typed methods |
| Error propagation | `{:error, reason}` tuples, linked processes | `error` interface with `fmt.Errorf` wrapping |
| Logging | `Logger` module | `log/slog` structured JSON logging |
| Template engine | Liquid-compatible (Solid) | `text/template` with `missingkey=error` |
| Leader election | Built-in with libcluster | `LocalElector` or `RaftElector` via `hashicorp/raft` + BoltDB |
| Retry timers | `Process.send_after` | `time.After` in select loop, checked on each tick |
| Workspace path safety | String prefix check | `filepath.Join` + `IsUnderRoot` canonicalization |
| Agent session | Single persistent Port per issue | Codex: persistent `exec.Cmd`; Claude: per-turn `exec.Cmd` |
| SSH execution | SSH connection via `sshex` | `golang.org/x/crypto/ssh` client with key-based auth |

## Package Layout

```
internal/
  agent/           -- Agent interface, registry, event types
    claude/         -- Claude Code adapter (NDJSON per-turn subprocess)
    codex/          -- Codex adapter (JSON-RPC persistent subprocess)
      protocol.go  -- JSON-RPC 2.0 encoding/decoding
      approval.go  -- Approval request handling
      dynamictool.go -- Dynamic tool execution (linear_graphql, plane_rest)
  agentrunner/     -- Runner: workspace + prompt + agent session lifecycle
  config/          -- Schema struct, YAML parsing, defaults, $VAR resolution, validation
  ha/              -- Elector interface
    local.go       -- LocalElector (single instance)
    raft.go        -- RaftElector (embedded hashicorp/raft + BoltDB)
  httpserver/      -- HTTP server, handlers, SSE
  orchestrator/    -- Poll loop, dispatch, reconcile, retry, state management
  prompt/          -- Template rendering with strict variable checking
  sshclient/       -- SSH client for remote execution
  tracker/         -- Tracker interface, registry, Issue model
    linear/        -- Linear GraphQL adapter
    plane/         -- Plane REST adapter
    memory/        -- In-memory adapter (for testing)
  workflow/        -- WORKFLOW.md loader (YAML front matter + prompt body)
  workspace/       -- Workspace creation, removal, hooks, path safety
cmd/
  symphony/        -- Main entry point
```

## Validation Rules

Config validation is performed at startup and fails fast with all errors joined:

- `tracker.kind` must be `"linear"` or `"plane"`
- `agent.kind` must be `"codex"` or `"claude"`
- `tracker.api_key` must be non-empty (after `$VAR` resolution)
- `tracker.linear.project_slug` required when kind=linear
- `tracker.plane.workspace_slug` required when kind=plane
- `tracker.plane.project_id` required when kind=plane

## Security Considerations

- Workspace paths are validated to stay under the configured root via canonical path comparison.
- Workspace directory names are sanitized to `[A-Za-z0-9._-]` (other characters replaced with `_`).
- Agent subprocesses are launched with cwd set to the per-issue workspace path.
- `$VAR` resolution reads environment variables; unresolved variables cause a startup error.
- API tokens and secret values are not logged.
- SSH host key verification is optional but recommended; it is skipped with a warning if not configured.
- Hook scripts run inside the workspace directory with a configurable timeout to prevent hanging.
- Approval policy for Codex actions defaults to `"auto"` (auto-approve all).
- Claude Code's `"auto"` permission mode maps to `--dangerously-skip-permissions`.
