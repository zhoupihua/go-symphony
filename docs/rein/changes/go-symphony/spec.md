# Spec: Go Symphony — Multi-Tracker, Multi-Agent Daemon

## Assumptions I'm Making

1. This is a CLI daemon (not a web service, not a library)
2. Configuration comes from WORKFLOW.md (YAML front matter + prompt body)
3. Plane uses REST API; Linear uses GraphQL API — both over HTTPS
4. Codex uses bidirectional JSON-RPC 2.0 over stdio; Claude Code uses one-shot `claude -p` subprocess
5. The orchestrator is the single authority for dispatch decisions (per SPEC.md §7)
6. Target scale: up to 10 concurrent agents by default (configurable)
7. Single binary for all deployment modes; etcd only needed in HA mode
8. Dashboard uses SSE for real-time updates (server → client), htmx for DOM updates, templ for type-safe HTML

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

WHEN Claude Code needs approval THEN `--dangerously-skip-permissions` or `--allowedTools` flags control behavior
- **TEST** `TestClaude_ApprovalFlags`

#### HA — Leader Election

WHEN `ha.enabled` is false or absent THEN the daemon uses `LocalElector` (always leader, no external dependencies)
- **TEST** `TestHA_LocalElectorAlwaysLeader`

WHEN `ha.enabled` is true THEN the daemon connects to etcd and campaigns for leadership
- **TEST** `TestHA_EtcdElectorCampaign`

WHEN an instance holds leadership and etcd lease expires THEN the instance steps down and stops orchestrating
- **TEST** `TestHA_LeaseLostStopsOrchestrating`

WHEN the leader process crashes THEN a standby instance acquires leadership via etcd campaign
- **TEST** `TestHA_FailoverOnLeaderCrash`

WHEN the leader resigns gracefully THEN it stops active agents and releases the etcd lease
- **TEST** `TestHA_GracefulResign`

WHEN `ha.enabled` is true but etcd is unreachable THEN the daemon fails startup with a clear error
- **TEST** `TestHA_EtcdUnreachableFails`

#### Orchestrator (SPEC.md §7, §8)

WHEN the daemon starts THEN it validates config, cleans up terminal workspaces, and schedules an immediate poll tick
- **TEST** `TestOrchestrator_Startup`

WHEN the daemon is a standby (not leader) THEN the orchestrator does not poll, dispatch, or reconcile
- **TEST** `TestOrchestrator_StandbyDoesNothing`

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

#### Prompt (SPEC.md §12)

WHEN rendering a prompt THEN the template is rendered with issue variables; unknown variables cause an error
- **TEST** `TestPrompt_RenderWithVariables`

WHEN prompt rendering fails THEN the run attempt fails and the orchestrator decides retry
- **TEST** `TestPrompt_RenderFailure`

#### Dashboard — Web UI

WHEN the HTTP server is enabled THEN the dashboard is served at `/` with real-time updates via SSE
- **TEST** `TestDashboard_ServedAtRoot`

WHEN the orchestrator state changes THEN an SSE event is pushed to connected dashboard clients
- **TEST** `TestDashboard_SSEPush`

WHEN a dashboard client connects THEN it receives the current state immediately
- **TEST** `TestDashboard_InitialStateOnConnect`

WHEN the dashboard shows running agents THEN it displays issue identifier, turn count, token usage, and duration
- **TEST** `TestDashboard_RunningAgentsDisplay`

WHEN the dashboard shows the retry queue THEN it displays issue identifier, attempt number, and retry countdown
- **TEST** `TestDashboard_RetryQueueDisplay`

#### Dashboard — API Endpoints

WHEN `GET /api/v1/state` is requested THEN it returns JSON with running, retrying, codex_totals, rate_limits
- **TEST** `TestHTTP_StateEndpoint`

WHEN `GET /api/v1/<issue_identifier>` is requested THEN it returns issue-specific runtime details
- **TEST** `TestHTTP_IssueEndpoint`

WHEN `POST /api/v1/refresh` is requested THEN it triggers an immediate poll
- **TEST** `TestHTTP_RefreshEndpoint`

WHEN `/healthz` is requested THEN the daemon responds with 200 if alive
- **TEST** `TestHTTP_HealthEndpoint`

#### Dashboard — Multi-Instance HA

WHEN a standby instance receives a dashboard request THEN it redirects to the leader's HTTP address
- **TEST** `TestDashboard_StandbyRedirectsToLeader`

WHEN a standby instance receives `/healthz` THEN it responds with 200 (health check works on all instances)
- **TEST** `TestDashboard_StandbyHealthCheck`

WHEN a standby instance receives `/api/v1/state` THEN it proxies or redirects to the leader
- **TEST** `TestDashboard_StandbyAPISRedirect`

WHEN the leader fails over THEN SSE clients reconnect to the new leader after a brief interruption
- **TEST** `TestDashboard_FailoverReconnect`

#### Observability (SPEC.md §13)

WHEN any operation occurs THEN structured logs include `issue_id`, `issue_identifier`, and `session_id` where applicable
- **TEST** `TestLogging_StructuredContext`

#### Deployment

WHEN running in a container THEN `slog` outputs JSON to stdout for log aggregation
- **TEST** `TestLogging_JSONFormat`

WHEN SIGTERM is received THEN the daemon gracefully stops active agents and exits
- **TEST** `TestGracefulShutdown`

WHEN `SYMPHONY_LOG_FORMAT=json` is set THEN slog uses JSON handler regardless of other config
- **TEST** `TestLogging_EnvVarOverride`

### Commands

```bash
# Build
cd go && go build -o symphony ./cmd/symphony

# Run (single-instance)
./symphony --workflow path/to/WORKFLOW.md

# Run (HA mode — etcd required)
./symphony --workflow path/to/WORKFLOW.md

# Run with explicit port
./symphony --workflow path/to/WORKFLOW.md --port 8080

# Test
go test ./...

# Test with coverage
go test -cover ./...

# Integration tests (require real API keys)
go test -tags=integration ./...

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
│       └── main.go                  # CLI entrypoint, signal handling
├── internal/
│   ├── workflow/
│   │   ├── workflow.go              # WORKFLOW.md loader (YAML front matter + prompt body)
│   │   ├── store.go                 # File watcher with last-known-good cache
│   │   └── workflow_test.go
│   ├── config/
│   │   ├── config.go                # Typed getters, defaults, $VAR resolution
│   │   ├── schema.go                # Config struct definitions
│   │   └── config_test.go
│   ├── orchestrator/
│   │   ├── orchestrator.go          # Poll loop, dispatch, reconcile, retry
│   │   ├── state.go                 # Runtime state (running, claimed, retry_attempts)
│   │   └── orchestrator_test.go
│   ├── tracker/
│   │   ├── tracker.go               # Tracker interface definition
│   │   ├── issue.go                 # Normalized Issue struct
│   │   ├── registry.go              # Adapter registry (name -> factory)
│   │   ├── memory/
│   │   │   ├── adapter.go           # In-memory adapter for testing
│   │   │   └── adapter_test.go
│   │   ├── linear/
│   │   │   ├── adapter.go           # Linear GraphQL adapter
│   │   │   ├── client.go            # GraphQL HTTP client
│   │   │   ├── adapter_test.go
│   │   │   └── client_test.go
│   │   └── plane/
│   │       ├── adapter.go           # Plane REST adapter
│   │       ├── client.go            # REST HTTP client
│   │       ├── adapter_test.go
│   │       └── client_test.go
│   ├── agent/
│   │   ├── agent.go                 # Agent interface definition
│   │   ├── event.go                 # Event types (TurnCompleted, TurnFailed, etc.)
│   │   ├── registry.go              # Adapter registry (name -> factory)
│   │   ├── codex/
│   │   │   ├── adapter.go           # Codex app-server adapter (JSON-RPC 2.0)
│   │   │   ├── protocol.go          # JSON-RPC 2.0 protocol types
│   │   │   ├── approval.go          # Auto-approval logic
│   │   │   ├── dynamictool.go       # Client-side tool handler (linear_graphql, plane_rest)
│   │   │   ├── adapter_test.go
│   │   │   └── protocol_test.go
│   │   └── claude/
│   │       ├── adapter.go           # Claude Code adapter (one-shot subprocess per turn)
│   │       ├── stream.go            # NDJSON stream parser
│   │       ├── adapter_test.go
│   │       └── stream_test.go
│   ├── ha/
│   │   ├── elector.go               # Elector interface definition
│   │   ├── local.go                 # LocalElector — always leader, no deps
│   │   ├── etcd.go                  # EtcdElector — etcd leader election
│   │   ├── elector_test.go
│   │   └── etcd_test.go
│   ├── workspace/
│   │   ├── workspace.go             # Workspace create/remove, hooks, path safety
│   │   ├── pathsafety.go            # Symlink-aware canonical path resolution
│   │   └── workspace_test.go
│   ├── prompt/
│   │   ├── prompt.go                # Template rendering with issue variables
│   │   └── prompt_test.go
│   ├── agentrunner/
│   │   ├── runner.go                # Orchestrates workspace + prompt + agent session
│   │   └── runner_test.go
│   └── dashboard/
│       ├── server.go                # HTTP server setup
│       ├── handlers.go              # API handlers (/api/v1/state, /api/v1/refresh, /healthz)
│       ├── sse.go                   # SSE event broker (fan-out to connected clients)
│       ├── middleware.go             # Leader redirect middleware for HA
│       ├── templ.go                 # Generated templ output
│       ├── components/
│       │   ├── layout.templ         # HTML layout shell
│       │   ├── dashboard.templ      # Main dashboard page
│       │   ├── running.templ        # Running agents panel
│       │   ├── retry.templ          # Retry queue panel
│       │   └── stats.templ          # Token stats panel
│       ├── static/
│       │   └── htmx.min.js          # Embedded htmx (via go:embed)
│       ├── server_test.go
│       └── sse_test.go
├── docs/
│   ├── SPEC.md                      # Copy of root SPEC.md (immutable reference)
│   └── SPEC-GO.md                   # Go-specific extensions beyond SPEC.md
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

### Code Style

```go
// Elector interface — single vs HA
type Elector interface {
    Campaign(ctx context.Context) error
    IsLeader() bool
    Resign()
    Done() <-chan struct{}
    LeaderAddr() string
}

// LocalElector — single instance, zero external deps
type LocalElector struct{ addr string }
func (e *LocalElector) Campaign(_ context.Context) error { return nil }
func (e *LocalElector) IsLeader() bool                   { return true }
func (e *LocalElector) Resign()                          {}
func (e *LocalElector) Done() <-chan struct{}             { return neverCloseCh }
func (e *LocalElector) LeaderAddr() string               { return e.addr }

// EtcdElector — HA mode via etcd
type EtcdElector struct { client, lease, key, addr string }
func (e *EtcdElector) Campaign(ctx context.Context) error { /* etcd campaign */ }
func (e *EtcdElector) IsLeader() bool                     { /* check lease */ }
func (e *EtcdElector) Resign()                            { /* revoke lease */ }
func (e *EtcdElector) Done() <-chan struct{}              { /* lease lost channel */ }
func (e *EtcdElector) LeaderAddr() string                 { /* read from etcd key */ }

// Tracker interface — pluggable adapters
type Tracker interface {
    FetchCandidateIssues(ctx context.Context) ([]Issue, error)
    FetchIssuesByStates(ctx context.Context, states []string) ([]Issue, error)
    FetchIssueStatesByIDs(ctx context.Context, ids []string) ([]Issue, error)
    CreateComment(ctx context.Context, issueID, body string) error
    UpdateIssueState(ctx context.Context, issueID, state string) error
}

// Agent/Session interface — pluggable adapters
type Agent interface {
    StartSession(ctx context.Context, opts SessionOptions) (Session, error)
}

type Session interface {
    RunTurn(ctx context.Context, prompt string, opts TurnOptions) (TurnResult, error)
    Close() error
}

// Orchestrator depends only on interfaces
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

// Dashboard SSE broker — pushes orchestrator state to browser
type SSEBroker struct {
    subscribers map[chan []byte]struct{}
    mu          sync.Mutex
}

// Error wrapping
func (a *LinearAdapter) FetchCandidateIssues(ctx context.Context) ([]Issue, error) {
    resp, err := a.client.Query(ctx, candidateQuery, vars)
    if err != nil {
        return nil, fmt.Errorf("linear: fetch candidates: %w", err)
    }
    // ...
}
```

### Testing Strategy

- **Framework**: Go standard `testing` package + `testify` for assertions
- **Test locations**: `*_test.go` alongside source files
- **Coverage target**: >= 80% for new code; 100% for interface contracts, path safety, and elector logic
- **Test levels**:
  - **Unit**: Each adapter independently with mocked HTTP servers (`httptest`); `LocalElector` for orchestrator tests
  - **HA**: `EtcdElector` tests with embedded etcd (`go.etcd.io/etcd/tests/v3`) or mocked etcd client
  - **Integration**: Tracker adapters against real API (optional, tagged `//go:build integration`)
  - **End-to-end**: Orchestrator with in-memory tracker + mock agent + `LocalElector`
  - **Dashboard**: `httptest` server with SSE client; verify real-time updates
- **Mocking**: Hand-written mocks implementing Tracker/Agent/Elector interfaces; no mockgen

### Boundaries

- **Always**: Use context.Context as first param; wrap errors with `fmt.Errorf`; log with `slog`; sanitize workspace paths; validate config at startup; check `elector.IsLeader()` before dispatch
- **Ask first**: Adding new dependencies beyond stdlib; changing Tracker/Agent/Elector interfaces; modifying SPEC.md conformance behavior
- **Never**: Run agent commands in the source repo; log secrets/API keys; use `init()` for adapter registration (explicit registration only); modify root SPEC.md; connect to etcd in single-instance mode

## Decisions

**Decision:** Use Go interfaces + explicit registry for adapter selection — **Rationale:** Go has no inheritance; interfaces are satisfied implicitly. A registry (`map[string]FactoryFunc`) lets WORKFLOW.md `tracker.kind`/`agent.kind` select adapters at startup without import side effects. Explicit is better than `init()` magic.

**Decision:** Codex adapter uses persistent JSON-RPC session; Claude Code adapter uses per-turn subprocess — **Rationale:** These are fundamentally different protocols. The Agent/Session interface abstracts this: Codex's `RunTurn` reuses the same subprocess; Claude Code's `RunTurn` spawns a new one. The orchestrator sees the same interface either way.

**Decision:** Dynamic tools are adapter-specific — **Rationale:** Codex supports `dynamicTools` in its JSON-RPC protocol. Claude Code has no equivalent. The Codex adapter handles `linear_graphql` and `plane_rest` as dynamic tools. The Claude Code adapter injects equivalent tool access via system prompt instructions. This keeps the Agent interface clean.

**Decision:** `agent.kind` is a new WORKFLOW.md front-matter key — **Rationale:** SPEC.md only defines `tracker.kind` and `codex.*` config. Adding `agent.kind` with values `"codex"` or `"claude"` lets the config select the agent adapter. For backwards compatibility, if `agent.kind` is absent but `codex.command` is present, default to `"codex"`.

**Decision:** Tracker-specific config is nested under `tracker.<kind>.*` — **Rationale:** Each tracker needs different config keys (Linear: `project_slug`; Plane: `workspace_slug`, `project_id`). The pattern is: `tracker.kind` selects the adapter, then `tracker.linear.*` or `tracker.plane.*` provides adapter-specific config. Only the active tracker's config section is validated.

**Decision:** `Elector` interface abstracts leadership; `LocalElector` for single-instance, `EtcdElector` for HA — **Rationale:** Single-instance deployment must not require etcd. The Elector interface lets the orchestrator code be identical for both modes. `LocalElector` always returns `IsLeader()=true` and never connects to any external service. `EtcdElector` uses etcd campaign + lease for leader election.

**Decision:** Standby instances redirect dashboard/API requests to leader — **Rationale:** Only the leader has orchestrator state. Rather than replicating state to standbys (adds complexity and latency), standbys simply redirect HTTP requests to the leader's address (obtained from etcd). `/healthz` still works on all instances for K8s health checks.

**Decision:** Dashboard uses templ + htmx + SSE — **Rationale:** templ provides type-safe, compile-checked HTML templates in Go. htmx enables dynamic DOM updates without a JS framework. SSE provides real-time server→client push. All three are pure Go (no npm, no build step). Single binary includes all static assets via `go:embed`.

**Decision:** slog with JSON handler for cloud, text handler for local — **Rationale:** Single logging interface. `SYMPHONY_LOG_FORMAT=json` env var switches to JSON. Default is text (human-readable for local development).

**Decision:** HTTP server with `net/http` from stdlib — **Rationale:** The observability API + dashboard + SSE has ~5 endpoints. No framework needed. `net/http` is sufficient and has zero dependencies.

## Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent interface cannot cleanly abstract Codex bidirectional vs Claude Code one-shot protocols | High | Design the interface around `Session` + `RunTurn` where each implementation manages its own subprocess lifecycle. Validate with both adapters before building orchestrator. |
| Plane REST API pagination or filtering differs from assumed behavior | Medium | Write Plane adapter integration tests early. Read Plane API docs carefully. Use `httptest` for unit tests against recorded responses. |
| Claude Code `stream-json` format changes or is undocumented | Medium | Pin minimum Claude Code version in docs. Write stream parser defensively with unknown-field tolerance. |
| etcd lease expiry causes false leader loss under network partition | High | Set lease TTL generously (default 10s). Use etcd `WithRequireLeader` for campaign. Log leadership transitions at WARN level. |
| Config schema becomes too complex with tracker-specific, agent-specific, and HA sections | Medium | Keep config flat: `tracker.kind` selects tracker sub-config, `agent.kind` selects agent sub-config, `ha.enabled` selects elector. Only active sections are validated. |
| Adding future agents (Devin, Aider, OpenHands) reveals interface gaps | High | Design the Agent interface with extension points: `SessionOptions` is a struct, not positional args. New fields can be added without breaking existing adapters. |
| SSE connections accumulate on leader under many dashboard clients | Low | Set reasonable keepalive interval. Drop stale connections on ping timeout. Dashboard is for operators, not high-traffic. |
| Performance at max concurrency (10 agents, each with subprocess + HTTP polling) | Low | Go handles this easily. Profile only if issues arise. |

## SPEC-GO Extensions (beyond SPEC.md)

These are Go-specific extensions documented in `go/docs/SPEC-GO.md`:

1. **`agent.kind` config key** — values: `"codex"`, `"claude"`. Defaults to `"codex"` for backwards compatibility.
2. **Plane tracker adapter** — `tracker.kind: "plane"` with Plane-specific config.
3. **`tracker.plane.*` config section** — `workspace_slug` (required), `project_id` (required, UUID), `api_key` (required, or `$VAR`), `endpoint` (default `https://api.plane.so/api/`).
4. **`ha.*` config section** — `enabled` (default `false`), `etcd_endpoints` (required when enabled), `lease_ttl_ms` (default `10000`), `advertise_addr` (required when enabled, HTTP address for dashboard redirect).
5. **`SYMPHONY_LOG_FORMAT` env var** — `"json"` or `"text"` (default `"text"`).
6. **`/healthz` endpoint** — always available when HTTP server is running, even on standbys.
7. **Graceful shutdown** — SIGTERM/SIGINT triggers context cancellation; agents receive shutdown signal.
8. **Web UI dashboard** — templ + htmx + SSE at `/`. Real-time updates without page refresh.
9. **Agent adapter registry** — extensible via code, not config; new agents implement the interface and register.
10. **Standby redirect** — non-leader instances redirect dashboard and API requests to the leader's `advertise_addr`.
