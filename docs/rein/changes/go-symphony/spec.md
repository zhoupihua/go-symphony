# Spec: Go Symphony — Multi-Tracker, Multi-Agent Daemon

## Assumptions I'm Making

1. This is a CLI daemon (not a web service, not a library)
2. Configuration comes from WORKFLOW.md (YAML front matter + prompt body)
3. Plane uses REST API; Linear uses GraphQL API — both over HTTPS
4. Codex uses bidirectional JSON-RPC 2.0 over stdio; Claude Code uses one-shot `claude -p` subprocess
5. The orchestrator is the single authority for dispatch decisions (per SPEC.md §7)
6. Target scale: up to 10 concurrent agents by default (configurable)
7. Deployment: single binary; single-instance needs zero external deps, HA needs etcd
8. Agent execution: local subprocess, SSH remote subprocess, or API call — selected by config
9. All agents are CLI-based (Codex, Claude Code); future API-based agents (Devin, OpenHands) extend the same interface
10. One instance per project; multi-project supported by running multiple instances with different WORKFLOW.md configs

## Requirements

### Success Criteria

#### Workflow & Config (SPEC.md §5, §6)

WHEN the daemon starts THEN it loads WORKFLOW.md from the configured path or cwd
- **TEST** `TestWorkflow_LoadFromFile`

WHEN WORKFLOW.md has YAML front matter THEN config values are parsed with defaults applied
- **TEST** `TestConfig_ParseWithDefaults`

WHEN a config value contains `$VAR_NAME` THEN it is resolved from environment variables
- **TEST** `TestConfig_ResolveEnvVar`

WHEN WORKFLOW.md changes on disk THEN the daemon reloads config without restart and re-applies live settings (poll interval, concurrency, states, prompt)
- **TEST** `TestWorkflowStore_DetectsFileChange`

WHEN a reloaded WORKFLOW.md is invalid THEN the daemon keeps the last known good config and logs an error
- **TEST** `TestWorkflowStore_InvalidReloadKeepsLastGood`

#### Tracker Interface

WHEN `tracker.kind` is "linear" THEN the Linear adapter is used for all tracker operations
- **TEST** `TestTracker_LinearAdapterSelected`

WHEN `tracker.kind` is "plane" THEN the Plane adapter is used for all tracker operations
- **TEST** `TestTracker_PlaneAdapterSelected`

WHEN an unknown `tracker.kind` is configured THEN the daemon fails startup with a clear error
- **TEST** `TestTracker_UnknownKindFails`

WHEN fetching candidate issues THEN the adapter returns normalized `Issue` structs regardless of tracker backend
- **TEST** `TestTracker_NormalizedIssues`

WHEN tracker API call fails on candidate fetch THEN the daemon logs the error and skips dispatch for this tick
- **TEST** `TestTracker_FetchFailureSkipsDispatch`

#### Linear Adapter

WHEN fetching candidates from Linear THEN the adapter queries GraphQL with project slug filter and active state filter
- **TEST** `TestLinear_FetchCandidates`

WHEN Linear API returns paginated results THEN the adapter follows all pages
- **TEST** `TestLinear_Pagination`

WHEN normalizing a Linear issue THEN labels are lowercased, priority mapped to integer, blocked_by extracted from inverse relations
- **TEST** `TestLinear_NormalizeIssue`

#### Plane Adapter

WHEN fetching candidates from Plane THEN the adapter queries REST API with workspace slug + project ID and state group filter
- **TEST** `TestPlane_FetchCandidates`

WHEN Plane API returns paginated results THEN the adapter follows offset/limit pagination
- **TEST** `TestPlane_Pagination`

WHEN normalizing a Plane issue THEN state group maps to active/terminal, priority string maps to integer (urgent=1, high=2, medium=3, low=4, none=0)
- **TEST** `TestPlane_NormalizeIssue`

WHEN Plane API endpoint is configured (self-hosted) THEN the adapter uses the custom base URL
- **TEST** `TestPlane_CustomEndpoint`

#### Agent Interface

WHEN `agent.kind` is "codex" THEN the Codex adapter is used for agent execution
- **TEST** `TestAgent_CodexAdapterSelected`

WHEN `agent.kind` is "claude" THEN the Claude Code adapter is used for agent execution
- **TEST** `TestAgent_ClaudeAdapterSelected`

WHEN an unknown `agent.kind` is configured THEN the daemon fails startup with a clear error
- **TEST** `TestAgent_UnknownKindFails`

WHEN an agent session starts THEN it creates a workspace, builds a prompt, and begins execution
- **TEST** `TestAgent_SessionStarts`

WHEN an agent session completes normally THEN it reports completion with token usage
- **TEST** `TestAgent_SessionCompletes`

WHEN an agent session fails THEN it reports the error category for retry decision
- **TEST** `TestAgent_SessionFails`

#### Agent Execution Modes

WHEN `worker.ssh_hosts` is not configured THEN agents run as local subprocesses via `os/exec`
- **TEST** `TestAgent_LocalExecution`

WHEN `worker.ssh_hosts` is configured THEN agents run on remote hosts via SSH
- **TEST** `TestAgent_SSHExecution`

WHEN a Codex agent runs over SSH THEN it maintains a persistent SSH session with bidirectional JSON-RPC
- **TEST** `TestCodex_SSHSession`

WHEN a Claude Code agent runs over SSH THEN each turn opens a new SSH command invocation
- **TEST** `TestClaude_SSHTurnInvocation`

WHEN `worker.ssh_hosts` has multiple hosts THEN the orchestrator selects the least-loaded host
- **TEST** `TestWorkerHost_Selection`

#### Codex Adapter

WHEN running a Codex session THEN the adapter starts `codex app-server` subprocess and communicates via JSON-RPC 2.0 over stdio
- **TEST** `TestCodex_StartSession`

WHEN Codex sends an approval request THEN the adapter auto-approves based on configured approval policy
- **TEST** `TestCodex_ApprovalHandling`

WHEN a Codex turn completes THEN the adapter reports turn result and starts continuation turn if issue is still active
- **TEST** `TestCodex_TurnCompletion`

WHEN Codex requests a dynamic tool call (e.g., `linear_graphql` or `plane_rest`) THEN the adapter executes it and returns the result
- **TEST** `TestCodex_DynamicTool`

#### Claude Code Adapter

WHEN running a Claude Code session THEN the adapter starts `claude -p --output-format stream-json` subprocess
- **TEST** `TestClaude_StartSession`

WHEN Claude Code streams NDJSON output THEN the adapter parses events and reports progress
- **TEST** `TestClaude_StreamParsing`

WHEN a Claude Code invocation completes THEN the adapter reports result and starts a new invocation for continuation if issue is still active
- **TEST** `TestClaude_Continuation`

WHEN Claude Code runs in auto mode THEN `--dangerously-skip-permissions` or `--allowedTools` flags control approval behavior
- **TEST** `TestClaude_ApprovalFlags`

#### HA (High Availability)

WHEN `ha.enabled` is false or not configured THEN `LocalElector` is used; the daemon is always leader, no external dependencies
- **TEST** `TestHA_LocalElector`

WHEN `ha.enabled` is true THEN `EtcdElector` campaigns for leadership via etcd
- **TEST** `TestHA_EtcdElector`

WHEN the daemon loses leadership THEN the orchestrator stops dispatching and existing agents are terminated gracefully
- **TEST** `TestHA_LeadershipLost`

WHEN a standby instance gains leadership THEN it performs startup cleanup (terminal workspace removal) and begins polling
- **TEST** `TestHA_Failover`

WHEN `ha.enabled` is true and etcd is unreachable THEN the daemon fails startup with a clear error
- **TEST** `TestHA_EtcdUnreachable`

#### Dashboard

WHEN the HTTP server is enabled THEN the Web UI dashboard is accessible at `/`
- **TEST** `TestDashboard_WebUI`

WHEN the dashboard loads THEN it shows running agents, retry queue, token usage, and rate limits
- **TEST** `TestDashboard_Content`

WHEN the orchestrator state changes THEN the dashboard receives real-time updates via SSE
- **TEST** `TestDashboard_SSEUpdates`

WHEN a standby instance's dashboard is accessed THEN it redirects to the leader's dashboard
- **TEST** `TestDashboard_StandbyRedirect`

WHEN `/api/v1/state` is requested THEN JSON with running, retrying, codex_totals, rate_limits is returned
- **TEST** `TestHTTP_StateEndpoint`

WHEN `POST /api/v1/refresh` is called THEN an immediate poll is triggered
- **TEST** `TestHTTP_RefreshEndpoint`

WHEN `/healthz` is requested THEN 200 is returned if alive
- **TEST** `TestHTTP_HealthEndpoint`

#### Orchestrator (SPEC.md §7, §8)

WHEN the daemon starts THEN it campaigns for leadership, validates config, cleans up terminal workspaces, and schedules an immediate poll tick
- **TEST** `TestOrchestrator_Startup`

WHEN a poll tick fires THEN the orchestrator reconciles running issues, validates dispatch preflight, fetches candidates, and dispatches eligible issues
- **TEST** `TestOrchestrator_PollTick`

WHEN dispatching an issue THEN the orchestrator checks global and per-state concurrency limits
- **TEST** `TestOrchestrator_ConcurrencyLimits`

WHEN an agent worker exits normally THEN the orchestrator schedules a continuation retry (~1 second)
- **TEST** `TestOrchestrator_ContinuationRetry`

WHEN an agent worker exits with failure THEN the orchestrator schedules exponential backoff retry
- **TEST** `TestOrchestrator_FailureRetry`

WHEN a running issue transitions to terminal state on the tracker THEN the orchestrator terminates the agent and cleans up the workspace
- **TEST** `TestOrchestrator_ReconciliationTerminal`

WHEN stall timeout is exceeded THEN the orchestrator terminates the stalled agent
- **TEST** `TestOrchestrator_StallDetection`

#### Workspace (SPEC.md §9)

WHEN creating a workspace THEN the path is `<root>/<sanitized_identifier>` and the identifier is sanitized to `[A-Za-z0-9._-]`
- **TEST** `TestWorkspace_SanitizedPath`

WHEN a workspace is newly created THEN the `after_create` hook runs; on failure, creation is aborted
- **TEST** `TestWorkspace_AfterCreateHook`

WHEN running an agent THEN `before_run` hook runs before the agent; on failure, the attempt is aborted
- **TEST** `TestWorkspace_BeforeRunHook`

WHEN an agent finishes THEN `after_run` hook runs regardless of outcome; failure is logged and ignored
- **TEST** `TestWorkspace_AfterRunHook`

WHEN the workspace path escapes the workspace root THEN the operation is rejected
- **TEST** `TestWorkspace_PathSafety`

WHEN a worker host is configured THEN workspace operations (create, remove, hooks) execute on the remote host via SSH
- **TEST** `TestWorkspace_SSHOperations`

#### Prompt (SPEC.md §12)

WHEN rendering a prompt THEN the template is rendered with issue variables; unknown variables cause an error
- **TEST** `TestPrompt_RenderWithVariables`

WHEN prompt rendering fails THEN the run attempt fails and the orchestrator decides retry
- **TEST** `TestPrompt_RenderFailure`

#### Observability (SPEC.md §13)

WHEN any operation occurs THEN structured logs include `issue_id`, `issue_identifier`, and `session_id` where applicable
- **TEST** `TestLogging_StructuredContext`

#### Deployment

WHEN running in a container THEN `slog` outputs JSON to stdout for log aggregation
- **TEST** `TestLogging_JSONFormat`

WHEN SIGTERM is received THEN the daemon gracefully stops active agents and exits
- **TEST** `TestGracefulShutdown`

### Commands

```bash
# Build
cd go && go build -o symphony ./cmd/symphony

# Run (single instance, no etcd)
./symphony --workflow path/to/WORKFLOW.md

# Run (HA mode, etcd required)
./symphony --workflow path/to/WORKFLOW.md  # ha.enabled: true in WORKFLOW.md

# Test
go test ./...

# Test with coverage
go test -cover ./...

# Lint
go vet ./...

# Format
gofmt -s -w .

# Generate templ files
go generate ./...
```

### Project Structure

```
go/
├── cmd/
│   └── symphony/
│       └── main.go                    # CLI entrypoint
├── internal/
│   ├── workflow/
│   │   ├── workflow.go                # WORKFLOW.md loader (YAML front matter + prompt body)
│   │   ├── store.go                   # File watcher with last-known-good cache
│   │   └── workflow_test.go
│   ├── config/
│   │   ├── config.go                  # Typed getters, defaults, $VAR resolution
│   │   ├── schema.go                  # Config struct definitions
│   │   └── config_test.go
│   ├── orchestrator/
│   │   ├── orchestrator.go            # Poll loop, dispatch, reconcile, retry
│   │   ├── state.go                   # Runtime state (running, claimed, retry_attempts)
│   │   └── orchestrator_test.go
│   ├── tracker/
│   │   ├── tracker.go                 # Tracker interface definition
│   │   ├── issue.go                   # Normalized Issue struct
│   │   ├── registry.go                # Adapter registry (name -> factory)
│   │   ├── memory/
│   │   │   ├── adapter.go             # In-memory adapter for testing
│   │   │   └── adapter_test.go
│   │   ├── linear/
│   │   │   ├── adapter.go             # Linear GraphQL adapter
│   │   │   ├── client.go              # GraphQL HTTP client
│   │   │   ├── adapter_test.go
│   │   │   └── client_test.go
│   │   └── plane/
│   │       ├── adapter.go             # Plane REST adapter
│   │       ├── client.go              # REST HTTP client
│   │       ├── adapter_test.go
│   │       └── client_test.go
│   ├── agent/
│   │   ├── agent.go                   # Agent + Session interface definitions
│   │   ├── event.go                   # Event types (TurnCompleted, TurnFailed, etc.)
│   │   ├── registry.go                # Adapter registry (name -> factory)
│   │   ├── codex/
│   │   │   ├── adapter.go             # Codex app-server adapter (JSON-RPC 2.0)
│   │   │   ├── protocol.go            # JSON-RPC 2.0 protocol types
│   │   │   ├── approval.go            # Auto-approval logic
│   │   │   ├── dynamictool.go         # Client-side tool handler (linear_graphql, plane_rest)
│   │   │   ├── adapter_test.go
│   │   │   └── protocol_test.go
│   │   └── claude/
│   │       ├── adapter.go             # Claude Code adapter (one-shot subprocess per turn)
│   │       ├── stream.go              # NDJSON stream parser
│   │       ├── adapter_test.go
│   │       └── stream_test.go
│   ├── workspace/
│   │   ├── workspace.go               # Workspace create/remove, hooks, path safety
│   │   ├── pathsafety.go              # Symlink-aware canonical path resolution
│   │   ├── remote.go                  # SSH remote workspace operations
│   │   └── workspace_test.go
│   ├── prompt/
│   │   ├── prompt.go                  # Template rendering with issue variables
│   │   └── prompt_test.go
│   ├── agentrunner/
│   │   ├── runner.go                  # Orchestrates workspace + prompt + agent session
│   │   └── runner_test.go
│   ├── ha/
│   │   ├── elector.go                 # Elector interface
│   │   ├── local.go                   # LocalElector (single instance, no deps)
│   │   ├── etcd.go                    # EtcdElector (multi-instance HA)
│   │   ├── elector_test.go
│   │   └── etcd_test.go
│   ├── httpserver/
│   │   ├── server.go                  # HTTP server setup
│   │   ├── api.go                     # /api/v1/state, /api/v1/refresh, /api/v1/:id
│   │   ├── dashboard.go               # Dashboard HTML via templ
│   │   ├── sse.go                     # SSE endpoint for real-time updates
│   │   ├── health.go                  # /healthz
│   │   ├── static/                    # Embedded static assets (CSS, JS)
│   │   ├── templ.go                   # Generated templ output
│   │   ├── dashboard_templ.go         # templ template definitions
│   │   └── server_test.go
│   └── ssh/
│       ├── client.go                  # SSH client wrapper
│       └── client_test.go
├── docs/
│   ├── SPEC.md                        # Copy of root SPEC.md (immutable reference)
│   └── SPEC-GO.md                     # Go-specific extensions beyond SPEC.md
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

### Code Style

```go
// Core interfaces — adapters implement these
type Tracker interface {
    FetchCandidateIssues(ctx context.Context) ([]Issue, error)
    FetchIssuesByStates(ctx context.Context, states []string) ([]Issue, error)
    FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]Issue, error)
    CreateComment(ctx context.Context, issueID, body string) error
    UpdateIssueState(ctx context.Context, issueID, state string) error
}

type Agent interface {
    StartSession(ctx context.Context, opts SessionOptions) (Session, error)
}

type Session interface {
    RunTurn(ctx context.Context, prompt string, opts TurnOptions) (TurnResult, error)
    Close() error
}

type Elector interface {
    Campaign(ctx context.Context) error
    IsLeader() bool
    Resign()
    Done() <-chan struct{}
    LeaderAddr() string
}

// SessionOptions carries agent + execution config
type SessionOptions struct {
    WorkspacePath string
    WorkerHost    string   // "" = local os/exec, non-empty = SSH
    ApprovalPolicy string
    SandboxPolicy  string
    MaxTurns       int
    DynamicTools   []ToolSpec
}

// Error wrapping with context
func (a *LinearAdapter) FetchCandidateIssues(ctx context.Context) ([]Issue, error) {
    resp, err := a.client.Query(ctx, candidateQuery, vars)
    if err != nil {
        return nil, fmt.Errorf("linear: fetch candidates: %w", err)
    }
    // ...
}

// Orchestrator uses Elector — single or HA is transparent
func (o *Orchestrator) Run(ctx context.Context) error {
    if err := o.elector.Campaign(ctx); err != nil {
        return fmt.Errorf("campaign: %w", err)
    }
    defer o.elector.Resign()

    ticker := time.NewTicker(o.config.PollInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-o.elector.Done():
            return errors.New("leadership lost")
        case <-ticker.C:
            if o.elector.IsLeader() {
                o.tick(ctx)
            }
        }
    }
}

// Dashboard: standby redirects to leader
func (h *DashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if !h.elector.IsLeader() {
        leader := h.elector.LeaderAddr()
        http.Redirect(w, r, "http://"+leader+"/", 307)
        return
    }
    // Render dashboard with orchestrator state
}
```

### Configuration Schema (WORKFLOW.md extensions)

```yaml
# Base SPEC.md config (unchanged)
tracker:
  kind: linear                    # "linear" or "plane"
  api_key: $LINEAR_API_KEY
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Cancelled"]
  # Linear-specific
  linear:
    project_slug: "my-project"
    endpoint: "https://api.linear.app/graphql"  # default
  # Plane-specific
  plane:
    workspace_slug: "my-team"
    project_id: "uuid-here"
    endpoint: "https://api.plane.so/api/"       # default

# Agent selection (SPEC-GO extension)
agent:
  kind: codex                     # "codex" or "claude"
  codex:
    command: "codex app-server"
    approval_policy: "never"
  claude:
    command: "claude"
    permission_mode: "fullAuto"   # default for non-interactive

# Worker hosts (SPEC.md Appendix A)
worker:
  ssh_hosts:
    - host: worker1.example.com:22
      max_concurrent_agents: 5
    - host: worker2.example.com:22
      max_concurrent_agents: 5

# HA configuration (SPEC-GO extension)
ha:
  enabled: false                  # true = etcd leader election required
  etcd_endpoints: ["http://etcd:2379"]
  lease_ttl_ms: 10000
  advertise_addr: "symphony-1:8080"  # this instance's HTTP address (for leader discovery)

# Existing SPEC.md config
polling:
  interval_ms: 30000
workspace:
  root: ~/symphony_workspaces
hooks:
  after_create: "..."
  before_run: "..."
  after_run: "..."
  before_remove: "..."
  timeout_ms: 60000
```

### Deployment Modes

| Mode | ha.enabled | worker.ssh_hosts | etcd | Notes |
|------|-----------|-----------------|------|-------|
| Local single | false | empty | no | Everything on one machine |
| Cloud single | false | empty | no | Same as local, on a cloud VM |
| Cloud + SSH workers | false | configured | no | Orchestrator on cloud, agents on SSH hosts |
| Cloud HA | true | empty | yes | Active-standby, agents on same machine |
| Cloud HA + SSH | true | configured | yes | Full distributed: HA + remote workers |

### Testing Strategy

- **Framework**: Go standard `testing` package + `testify` for assertions
- **Test locations**: `*_test.go` alongside source files
- **Coverage target**: >= 80% for new code; 100% for interface contracts, path safety, and elector logic
- **Test levels**:
  - **Unit**: Each adapter independently with mocked HTTP servers (`httptest`) and mocked SSH
  - **Integration**: Tracker adapters against real API (optional, tagged `//go:build integration`)
  - **End-to-end**: Orchestrator with in-memory tracker + mock agent
  - **HA**: EtcdElector with etcd test container
- **Mocking**: Hand-written mocks implementing Tracker/Agent/Elector interfaces; no mockgen

### Boundaries

- **Always**: Use context.Context as first param; wrap errors with `fmt.Errorf`; log with `slog`; sanitize workspace paths; validate config at startup
- **Ask first**: Adding new dependencies beyond stdlib; changing Tracker/Agent/Elector interfaces; modifying SPEC.md conformance behavior
- **Never**: Run agent commands in the source repo; log secrets/API keys; use `init()` for adapter registration (explicit registration only); modify root SPEC.md

## Decisions

**Decision:** Pure Go, no frameworks — **Rationale:** Symphony is a polling daemon, not a request-response service. stdlib + selective deps (etcd client, templ) covers all needs. No framework overhead.

**Decision:** Go interfaces + explicit registry for adapter selection — **Rationale:** Go interfaces are satisfied implicitly. A registry (`map[string]FactoryFunc`) lets WORKFLOW.md `tracker.kind`/`agent.kind` select adapters at startup without import side effects. Explicit is better than `init()` magic.

**Decision:** Codex uses persistent JSON-RPC session; Claude Code uses per-turn subprocess — **Rationale:** Fundamentally different protocols. The Agent/Session interface abstracts this: Codex's `RunTurn` reuses the same subprocess/connection; Claude Code's `RunTurn` spawns a new one. Orchestrator sees the same interface.

**Decision:** Execution mode (local vs SSH) is orthogonal to agent type — **Rationale:** Both Codex and Claude Code support local `os/exec` and SSH remote execution. The `SessionOptions.WorkerHost` field selects the mode. Agent adapters handle both internally; the interface doesn't change.

**Decision:** SSH Worker is Phase 1, not Phase 2 — **Rationale:** Cloud separated deployment requires SSH for CLI-based agents. Without it, cloud deployment can only be single-machine. SSH is essential for real-world cloud use.

**Decision:** Dynamic tools are agent-specific — **Rationale:** Codex supports `dynamicTools` in JSON-RPC protocol. Claude Code has no equivalent. Codex adapter handles `linear_graphql` and `plane_rest` as dynamic tools. Claude Code adapter injects equivalent access via system prompt or `--allowedTools`. This keeps the Agent interface clean.

**Decision:** `agent.kind` is a new WORKFLOW.md front-matter key — **Rationale:** SPEC.md only defines `tracker.kind` and `codex.*` config. Adding `agent.kind` with values `"codex"` or `"claude"` lets the config select the agent adapter. Backwards compatible: if `agent.kind` is absent but `codex.command` is present, default to `"codex"`.

**Decision:** Tracker-specific config nested under `tracker.<kind>.*` — **Rationale:** Each tracker needs different keys. `tracker.kind` selects the adapter; `tracker.linear.*` or `tracker.plane.*` provides adapter-specific config. Only the active tracker's config is validated.

**Decision:** Elector interface with LocalElector + EtcdElector — **Rationale:** Single-instance needs zero external deps (LocalElector always leader). HA uses etcd for leader election (EtcdElector). Config selects implementation. Same binary works for both.

**Decision:** Standby dashboard redirects to leader — **Rationale:** Only the leader has orchestrator state. Standby instances redirect HTTP requests to the leader's address (obtained from Elector.LeaderAddr()). /healthz works on all instances. No state sync needed.

**Decision:** Dashboard uses templ + htmx + SSE — **Rationale:** templ for type-safe Go HTML templates, htmx for declarative DOM updates, SSE for real-time server→client push. No JS framework, no build step, single binary via go:embed.

**Decision:** slog with configurable format — **Rationale:** `SYMPHONY_LOG_FORMAT=json` for cloud (container log aggregation), default text for local. Single logging interface.

**Decision:** HTTP server with `net/http` — **Rationale:** 5 endpoints (dashboard, state, refresh, health, SSE). No framework needed. stdlib is sufficient.

**Decision:** One instance per project; multi-project via multiple instances — **Rationale:** Simpler orchestrator state (no project-scoped concurrency tracking). Each instance gets its own WORKFLOW.md, tracker connection, and workspace root. Multiple instances managed externally (systemd, K8s). Future: `projects` array in WORKFLOW.md for single-instance multi-project (interface design will accommodate).

## Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent interface cannot cleanly abstract Codex bidirectional vs Claude Code one-shot | High | Session + RunTurn model validated against both adapters. Codex reuses connection, Claude creates new one per turn. |
| SSH remote execution has latency/reliability issues | Medium | Codex maintains persistent SSH session. Claude Code per-turn SSH adds overhead but is simpler. Both tested with network simulation. |
| Plane REST API pagination/filtering differs from assumed | Medium | Write Plane adapter integration tests early. Use httptest for unit tests against recorded responses. |
| Claude Code `stream-json` format changes or is undocumented | Medium | Pin minimum Claude Code version. Write stream parser defensively with unknown-field tolerance. |
| etcd client dependency bloats binary | Low | etcd client is ~5MB compiled. Only linked when ha.enabled=true at runtime. Acceptable for a server binary. |
| Adding future agents (Devin, OpenHands) reveals interface gaps | High | SessionOptions is a struct, not positional args. New fields added without breaking existing adapters. API-based agents get a new adapter implementing the same interface. |
| Dashboard SSE doesn't work behind some proxies | Medium | Document proxy configuration (disable buffering for SSE). Fallback: dashboard auto-polls if SSE disconnects. |

## SPEC-GO Extensions (beyond SPEC.md)

Documented in `go/docs/SPEC-GO.md`:

1. **`agent.kind` config key** — values: `"codex"`, `"claude"`. Defaults to `"codex"` when `codex.command` is present.
2. **`agent.claude.*` config section** — `command` (default `"claude"`), `permission_mode` (default `"fullAuto"`), `allowed_tools`, `max_turns`.
3. **Plane tracker adapter** — `tracker.kind: "plane"` with `tracker.plane.*` config.
4. **`ha.*` config section** — `enabled`, `etcd_endpoints`, `lease_ttl_ms`, `advertise_addr`.
5. **`SYMPHONY_LOG_FORMAT` env var** — `"json"` or `"text"` (default `"text"`).
6. **`/healthz` endpoint** — always available when HTTP server is running.
7. **Graceful shutdown** — SIGTERM/SIGINT triggers context cancellation.
8. **Web UI dashboard** — templ + htmx + SSE at `/`.
9. **SSE endpoint** — `/api/v1/events` for real-time state updates.
10. **Agent adapter registry** — extensible via code; new agents implement the interface and register.
11. **SSH remote execution** — `worker.ssh_hosts` config enables remote agent execution and workspace management.
12. **Multi-project** — one instance per project; multi-project by running multiple instances with different WORKFLOW.md. Future enhancement: `projects` array in WORKFLOW.md for single-instance multi-project.
