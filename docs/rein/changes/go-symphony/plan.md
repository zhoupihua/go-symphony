# Go Symphony Implementation Plan

> **For agentic workers:** This is your primary reference during implementation.
> Read task.md only for status tracking (which tasks are done).

**Goal:** Build a production-grade Go daemon that orchestrates coding agents (Codex, Claude Code) across multiple issue trackers (Linear, Plane), supporting local, cloud, and HA deployment.

**Architecture:** Configuration-driven composition (V5). Three core interfaces (`Tracker`, `Agent`/`Session`, `Elector`) with adapter implementations selected by WORKFLOW.md config. The orchestrator depends only on interfaces. Pure Go, no frameworks.

**Tech Stack:** Go 1.22+, `log/slog`, `net/http`, `os/exec`, `gopkg.in/yaml.v3`, `github.com/a-h/templ`, `go.etcd.io/etcd/client/v3`, `github.com/stretchr/testify`

---

## Architecture Overview

Symphony is a polling daemon with six layers. Data flows downward from the issue tracker into the orchestrator, which dispatches issues to agent runners that execute in isolated workspaces.

```
WORKFLOW.md ──→ WorkflowLoader ──→ Config
                                       │
                                       ▼
Issue Tracker ──→ Tracker Adapter ──→ Orchestrator ──→ AgentRunner
   (Linear/Plane)                                      │
                                                       ├── Workspace (create, hooks)
                                                       ├── PromptBuilder (render template)
                                                       └── Agent Adapter (Codex/Claude)
                                                              │
                                                              ▼
                                                         Subprocess (local/SSH)

Orchestrator state ──→ HTTP Server ──→ Dashboard (templ + htmx + SSE)
                          │
                          └── Elector (LocalElector / EtcdElector)
```

The orchestrator owns all runtime state: running agents, retry queue, claimed issues. It polls the tracker on a fixed interval, reconciles running issues against tracker state, dispatches new issues up to concurrency limits, and handles retries with exponential backoff.

The Elector interface makes HA transparent: `LocalElector` always leads (single instance), `EtcdElector` campaigns via etcd (multi-instance). The orchestrator checks `IsLeader()` before each tick.

Dashboard serves real-time updates via SSE. Standby instances redirect to the leader.

## Architecture Decisions

- **Interface-first, adapter-parallel**: Define Tracker/Agent/Elector interfaces before implementations. All adapters implement the same contracts.
- **Vertical slicing**: Build one complete path at a time (e.g., workflow → config → tracker → workspace → agent → orchestrator) rather than all of one layer.
- **Break Elixir Workflow/WorkflowStore circular dep**: In Go, `Workflow` is a pure parser function. `WorkflowStore` is a goroutine that calls Workflow.Load() and caches the result. No circular dependency.
- **Agent/Session split**: `Agent.StartSession()` returns a `Session`. `Session.RunTurn()` executes one turn. Codex reuses the same subprocess across turns; Claude Code creates a new subprocess per turn. The interface accommodates both.
- **SSH as SessionOptions field**: `SessionOptions.WorkerHost` determines local vs remote execution. Adapters handle both internally.
- **templ for HTML, htmx for interactions, SSE for real-time**: No JS framework, no build step, single binary via `go:embed`.

## Dependency Graph

```
Issue struct (tracker/issue.go)
    │
    ├── Tracker interface (tracker/tracker.go)
    │       ├── Memory adapter (tracker/memory/)
    │       ├── Linear adapter (tracker/linear/) ─── Linear Client
    │       └── Plane adapter (tracker/plane/) ──── Plane Client
    │
    ├── Agent + Session interfaces (agent/agent.go)
    │       ├── Codex adapter (agent/codex/) ────── JSON-RPC protocol
    │       └── Claude adapter (agent/claude/) ──── NDJSON stream parser
    │
    ├── Workspace (workspace/) ── PathSafety, SSH
    ├── PromptBuilder (prompt/)
    ├── AgentRunner (agentrunner/) ── Workspace + Prompt + Agent
    │
    ├── Workflow (workflow/) ── YAML parser
    ├── WorkflowStore (workflow/) ── File watcher + cache
    ├── Config (config/) ── Workflow + Schema
    │
    ├── Elector (ha/) ── LocalElector, EtcdElector
    │
    └── Orchestrator (orchestrator/) ── Config + Tracker + AgentRunner + Workspace + Elector
            │
            └── HTTP Server (httpserver/) ── Orchestrator state + Dashboard + SSE
```

## Vertical Slice Strategy

**Phase 1: Foundation** (tasks 1-3) — Project scaffold, workflow loading, config parsing. Can load and validate WORKFLOW.md.

**Phase 2: Tracker layer** (tasks 4-6) — Tracker interface, Linear adapter, Plane adapter. Can fetch issues from both trackers.

**Phase 3: Execution layer** (tasks 7-10) — Workspace, prompt, Agent interface, Codex adapter. Can create workspaces and run Codex sessions.

**Phase 4: Orchestration** (tasks 11-12) — AgentRunner, Orchestrator. Full core daemon: polls, dispatches, retries, reconciles.

**Phase 5: Second agent** (task 13) — Claude Code adapter. Validates Agent interface extensibility.

**Phase 6: Remote execution** (task 14) — SSH client, remote workspace, remote agent execution.

**Phase 7: HA** (task 15) — Elector interface, LocalElector, EtcdElector.

**Phase 8: Observability** (tasks 16-17) — HTTP API, Dashboard with SSE, healthz.

**Phase 9: CLI & deployment** (task 18) — CLI flags, signal handling, logging, graceful shutdown, docs.

Each phase produces working, testable software. Phases 2-5 can partially parallelize (tracker and agent adapters are independent).

## File Map

| File | Purpose | New/Modified |
|------|---------|-------------|
| `go/go.mod` | Module definition and dependencies | New |
| `go/cmd/symphony/main.go` | CLI entrypoint | New |
| `go/internal/workflow/workflow.go` | WORKFLOW.md loader (YAML front matter + prompt body) | New |
| `go/internal/workflow/store.go` | File watcher with last-known-good cache | New |
| `go/internal/workflow/workflow_test.go` | Workflow and store tests | New |
| `go/internal/config/config.go` | Typed getters, defaults, $VAR resolution | New |
| `go/internal/config/schema.go` | Config struct definitions with YAML/json tags | New |
| `go/internal/config/config_test.go` | Config parsing and resolution tests | New |
| `go/internal/tracker/tracker.go` | Tracker interface + Issue struct + registry | New |
| `go/internal/tracker/memory/adapter.go` | In-memory tracker for testing | New |
| `go/internal/tracker/memory/adapter_test.go` | Memory adapter tests | New |
| `go/internal/tracker/linear/adapter.go` | Linear GraphQL adapter | New |
| `go/internal/tracker/linear/client.go` | GraphQL HTTP client | New |
| `go/internal/tracker/linear/adapter_test.go` | Linear adapter tests | New |
| `go/internal/tracker/linear/client_test.go` | Linear client tests | New |
| `go/internal/tracker/plane/adapter.go` | Plane REST adapter | New |
| `go/internal/tracker/plane/client.go` | REST HTTP client | New |
| `go/internal/tracker/plane/adapter_test.go` | Plane adapter tests | New |
| `go/internal/tracker/plane/client_test.go` | Plane client tests | New |
| `go/internal/workspace/workspace.go` | Workspace create/remove, hooks | New |
| `go/internal/workspace/pathsafety.go` | Symlink-aware canonical path resolution | New |
| `go/internal/workspace/remote.go` | SSH remote workspace operations | New |
| `go/internal/workspace/workspace_test.go` | Workspace and path safety tests | New |
| `go/internal/prompt/prompt.go` | Template rendering with issue variables | New |
| `go/internal/prompt/prompt_test.go` | Prompt rendering tests | New |
| `go/internal/agent/agent.go` | Agent + Session interface + event types + registry | New |
| `go/internal/agent/codex/adapter.go` | Codex app-server adapter | New |
| `go/internal/agent/codex/protocol.go` | JSON-RPC 2.0 protocol types | New |
| `go/internal/agent/codex/approval.go` | Auto-approval logic | New |
| `go/internal/agent/codex/dynamictool.go` | Client-side tool handler | New |
| `go/internal/agent/codex/adapter_test.go` | Codex adapter tests | New |
| `go/internal/agent/codex/protocol_test.go` | Protocol types tests | New |
| `go/internal/agent/claude/adapter.go` | Claude Code adapter | New |
| `go/internal/agent/claude/stream.go` | NDJSON stream parser | New |
| `go/internal/agent/claude/adapter_test.go` | Claude adapter tests | New |
| `go/internal/agent/claude/stream_test.go` | Stream parser tests | New |
| `go/internal/agentrunner/runner.go` | Orchestrates workspace + prompt + agent | New |
| `go/internal/agentrunner/runner_test.go` | Runner tests | New |
| `go/internal/orchestrator/orchestrator.go` | Poll loop, dispatch, reconcile, retry | New |
| `go/internal/orchestrator/state.go` | Runtime state structs | New |
| `go/internal/orchestrator/orchestrator_test.go` | Orchestrator tests | New |
| `go/internal/ha/elector.go` | Elector interface | New |
| `go/internal/ha/local.go` | LocalElector | New |
| `go/internal/ha/etcd.go` | EtcdElector | New |
| `go/internal/ha/elector_test.go` | Elector tests | New |
| `go/internal/ha/etcd_test.go` | EtcdElector tests | New |
| `go/internal/httpserver/server.go` | HTTP server setup | New |
| `go/internal/httpserver/api.go` | /api/v1/state, /api/v1/refresh, /api/v1/:id | New |
| `go/internal/httpserver/sse.go` | SSE endpoint | New |
| `go/internal/httpserver/health.go` | /healthz | New |
| `go/internal/httpserver/dashboard_templ.go` | templ template definitions | New |
| `go/internal/httpserver/server_test.go` | HTTP server tests | New |
| `go/internal/ssh/client.go` | SSH client wrapper | New |
| `go/internal/ssh/client_test.go` | SSH client tests | New |
| `go/docs/SPEC.md` | Copy of root SPEC.md (immutable reference) | New |
| `go/docs/SPEC-GO.md` | Go-specific extensions | New |
| `go/Makefile` | Build, test, lint targets | New |
| `go/README.md` | Go implementation README | New |

## Task Details

### 1.1 Project scaffold and go.mod
- **Acceptance:** `go build ./...` succeeds; `go test ./...` runs (0 tests)
- **Verification:** `cd go && go build ./... && go test ./...`
- **Dependencies:** None
- **Files:** `go/go.mod`, `go/cmd/symphony/main.go`
- **Scope:** S
- **Approach:** Create `go/go.mod` with module path `github.com/ainative/go-symphony`. Create minimal `main.go` that prints version and exits. Add `gopkg.in/yaml.v3` and `github.com/stretchr/testify` dependencies.
- **Edge Cases:** None
- **Rollback:** Delete `go/go.mod` and `go/cmd/`

### 1.2 Core data types and interfaces
- **Acceptance:** All core interfaces (Tracker, Agent, Session, Elector) and data types (Issue, SessionOptions, TurnOptions, TurnResult, Event) compile. Tests verify interface satisfaction.
- **Verification:** `cd go && go build ./... && go test ./internal/tracker/... ./internal/agent/... ./internal/ha/...`
- **Dependencies:** 1.1
- **Files:** `go/internal/tracker/tracker.go`, `go/internal/tracker/issue.go`, `go/internal/tracker/registry.go`, `go/internal/agent/agent.go`, `go/internal/agent/event.go`, `go/internal/agent/registry.go`, `go/internal/ha/elector.go`
- **Scope:** M
- **Approach:** Define all interfaces and types as specified in spec.md Code Style section. Tracker has 5 methods. Agent has StartSession. Session has RunTurn + Close. Elector has Campaign, IsLeader, Resign, Done, LeaderAddr. Issue struct has all SPEC.md §4 fields. Event types: TurnCompleted, TurnFailed, TurnCancelled, TurnInputRequired, ApprovalAutoApproved, Notification, UsageReport. Registry is `map[string]FactoryFunc`.
- **Edge Cases:** Registry must handle duplicate registration gracefully (panic or error).
- **Rollback:** Delete the type files

### 1.3 Workflow loader
- **Acceptance:** Can parse WORKFLOW.md with YAML front matter and markdown body. Returns config map + prompt template string. Handles files without front matter. Handles empty files.
- **Verification:** `cd go && go test ./internal/workflow/...`
- **Dependencies:** 1.1
- **Files:** `go/internal/workflow/workflow.go`, `go/internal/workflow/workflow_test.go`
- **Scope:** M
- **Approach:** `Load(path string) (config map[string]any, prompt string, error)`. Split on `---` delimiters. Parse first section as YAML via `gopkg.in/yaml.v3`. Trim remaining as prompt template. If no front matter, entire file is prompt with empty config. Test with: valid file, no front matter, empty front matter, empty file, invalid YAML.
- **Edge Cases:** Front matter with non-map YAML (e.g., a list) must return error. Multiple `---` delimiters — only the first two delimit front matter.
- **Rollback:** Delete workflow package

### 2.1 Config schema and parsing
- **Acceptance:** Config struct parses from YAML map with all defaults applied. `$VAR_NAME` resolution works. Validation catches missing required fields.
- **Verification:** `cd go && go test ./internal/config/...`
- **Dependencies:** 1.2, 1.3
- **Files:** `go/internal/config/schema.go`, `go/internal/config/config.go`, `go/internal/config/config_test.go`
- **Scope:** L — split into 2.1a (schema + defaults) and 2.1b ($VAR resolution + validation)

### 2.1a Config schema struct definitions with defaults
- **Acceptance:** Schema struct has all fields from spec config section. JSON tags have `default=` values. `Parse(map) (*Schema, error)` applies defaults.
- **Verification:** `cd go && go test ./internal/config/... -run TestConfig_ParseWithDefaults`
- **Dependencies:** 1.2, 1.3
- **Files:** `go/internal/config/schema.go`, `go/internal/config/config.go`, `go/internal/config/config_test.go`
- **Scope:** M
- **Approach:** Nested structs: TrackerConfig (Kind, APIKey, ActiveStates, TerminalStates, Linear, Plane), PollingConfig (IntervalMS default 30000), WorkspaceConfig (Root default system-temp/symphony_workspaces), HooksConfig (AfterCreate, BeforeRun, AfterRun, BeforeRemove, TimeoutMS default 60000), AgentConfig (Kind, MaxConcurrent default 10, MaxTurns default 20, MaxRetryBackoffMS default 300000, MaxConcurrentByState, Codex, Claude), WorkerConfig (SSHHosts), HAConfig (Enabled, EtcdEndpoints, LeaseTTLMS, AdvertiseAddr), ServerConfig (Port, Host), CodexConfig (Command, ApprovalPolicy, ThreadSandbox, TurnSandboxPolicy, TurnTimeoutMS, ReadTimeoutMS, StallTimeoutMS), ClaudeConfig (Command default "claude", PermissionMode, AllowedTools, MaxTurns), LinearConfig (ProjectSlug, Endpoint), PlaneConfig (WorkspaceSlug, ProjectID, Endpoint). `Parse()` iterates struct fields, applies defaults from tags, validates required fields.
- **Edge Cases:** `agent.kind` absent but `codex.command` present → default to "codex". `ha.enabled` absent → false. `workspace.root` with `~` → expand home.
- **Rollback:** Delete config package

### 2.1b Config $VAR resolution and validation
- **Acceptance:** `$LINEAR_API_KEY` in config resolves to env var value. Missing env var returns clear error. `Validate()` checks: tracker.kind present/supported, tracker.api_key present after $ resolution, agent.kind present/supported.
- **Verification:** `cd go && go test ./internal/config/... -run TestConfig_Resolve`
- **Dependencies:** 2.1a
- **Files:** `go/internal/config/config.go`, `go/internal/config/config_test.go`
- **Scope:** S
- **Approach:** `resolveEnvVars(schema *Schema) error` walks all string fields. If value starts with `$`, resolve via `os.Getenv`. If env var empty, return error naming the field and var. `Validate()` checks: tracker.kind in registry, agent.kind in registry, api_key non-empty after resolution, project_slug non-empty for linear, workspace_slug + project_id non-empty for plane.
- **Edge Cases:** `$VAR` where VAR is not set → error. Value without `$` → left as-is. Nested struct fields need recursive walking.
- **Rollback:** Remove resolution and validation logic

### 2.2 WorkflowStore with file watcher
- **Acceptance:** Detects WORKFLOW.md file changes and reloads. Keeps last-known-good on reload failure. Returns current config via `Current()`.
- **Verification:** `cd go && go test ./internal/workflow/... -run TestWorkflowStore`
- **Dependencies:** 1.3, 2.1a
- **Files:** `go/internal/workflow/store.go`, `go/internal/workflow/workflow_test.go`
- **Scope:** M
- **Approach:** `Store` goroutine polls file mtime + size every 1s. On change, calls `Workflow.Load()`. If load succeeds, update cached config. If fails, log error and keep previous. `Current() (Config, string, error)` returns cached values. `ForceReload()` triggers immediate re-read. Shutdown via context cancellation.
- **Edge Cases:** File deleted temporarily (editor save) → keep last good, log warning. File permissions change → treat as change. First load fails → return error from Current().
- **Rollback:** Delete store.go

### 3.1 Tracker memory adapter
- **Acceptance:** Implements Tracker interface. Returns pre-configured issues. Supports all 5 methods.
- **Verification:** `cd go && go test ./internal/tracker/memory/...`
- **Dependencies:** 1.2
- **Files:** `go/internal/tracker/memory/adapter.go`, `go/internal/tracker/memory/adapter_test.go`
- **Scope:** S
- **Approach:** `MemoryAdapter` holds `[]Issue` in a mutex-protected slice. `FetchCandidateIssues` returns issues with state in active states. `FetchIssuesByStates` filters by state names. `FetchIssueStatesByIDs` filters by IDs. `CreateComment` and `UpdateIssueState` mutate in-memory state. Constructed with `NewMemoryAdapter(issues []Issue)`.
- **Edge Cases:** Empty issue list. State with mixed case (normalize to lowercase).
- **Rollback:** Delete memory package

### 3.2 Linear tracker adapter
- **Acceptance:** Fetches candidate issues from Linear GraphQL API with pagination. Normalizes responses to Issue struct.
- **Verification:** `cd go && go test ./internal/tracker/linear/...`
- **Dependencies:** 1.2, 3.1
- **Files:** `go/internal/tracker/linear/adapter.go`, `go/internal/tracker/linear/client.go`, `go/internal/tracker/linear/adapter_test.go`, `go/internal/tracker/linear/client_test.go`
- **Scope:** M
- **Approach:** `Client` wraps HTTP POST to Linear GraphQL endpoint with `Authorization: <api_key>` header. `Query(ctx, query, variables) (map, error)` handles request/response with 30s timeout. Pagination via `after` cursor, page size 50. `Adapter` implements Tracker interface, delegates to Client. Normalization: labels → lowercase, priority → integer (null if non-int), blocked_by from inverse relations of type "blocks", timestamps parse ISO-8601. Tests use `httptest.NewServer` with canned GraphQL responses.
- **Edge Cases:** Pagination cursor missing → error. Empty data array → return empty slice. Network timeout → return error. GraphQL errors in response → return error.
- **Rollback:** Delete linear package

### 3.3 Plane tracker adapter
- **Acceptance:** Fetches candidate issues from Plane REST API with offset/limit pagination. Normalizes responses to Issue struct. Supports custom endpoint for self-hosted.
- **Verification:** `cd go && go test ./internal/tracker/plane/...`
- **Dependencies:** 1.2, 3.1
- **Files:** `go/internal/tracker/plane/adapter.go`, `go/internal/tracker/plane/client.go`, `go/internal/tracker/plane/adapter_test.go`, `go/internal/tracker/plane/client_test.go`
- **Scope:** M
- **Approach:** `Client` wraps HTTP GET/POST/PATCH to Plane REST API with `Authorization: Bearer <api_key>` header. Base URL defaults to `https://api.plane.so/api/`, overridable for self-hosted. Fetch states endpoint returns state groups (backlog/unstarted/started/completed/cancelled). Map groups to active/terminal. `FetchCandidateIssues` filters by state group. Priority mapping: urgent=1, high=2, medium=3, low=4, none=0. Pagination via offset+limit (default 50). Tests use `httptest.NewServer`.
- **Edge Cases:** State group mapping for custom states (use group field, not name). UUID project_id validation. Empty results → empty slice.
- **Rollback:** Delete plane package

### 4.1 Workspace and path safety
- **Acceptance:** Creates workspace at `<root>/<sanitized_key>`. Sanitizes to `[A-Za-z0-9._-]`. Rejects paths escaping root. Runs lifecycle hooks with timeout.
- **Verification:** `cd go && go test ./internal/workspace/...`
- **Dependencies:** 1.2, 2.1a
- **Files:** `go/internal/workspace/workspace.go`, `go/internal/workspace/pathsafety.go`, `go/internal/workspace/workspace_test.go`
- **Scope:** M
- **Approach:** `SanitizeKey(identifier string) string` replaces non-[A-Za-z0-9._-] with `_`. `Canonicalize(path string) (string, error)` resolves symlinks via `filepath.EvalSymlinks`. `Create(ctx, root, identifier) (path string, createdNow bool, error)` computes path, validates it stays under root, ensures directory, marks if newly created. If newly created and after_create hook configured, run it with timeout. `Remove(ctx, path, hooks) error` runs before_remove hook then removes directory. `RunHook(ctx, script, workspace, timeout) error` executes `bash -lc <script>` with workspace as cwd. Hook failure semantics: after_create and before_run → abort; after_run and before_remove → log and ignore.
- **Edge Cases:** Symlink pointing outside root → reject. Concurrent create of same workspace → idempotent (directory already exists, createdNow=false). Hook timeout → kill process, return error.
- **Rollback:** Delete workspace package

### 4.2 Prompt builder
- **Acceptance:** Renders prompt template with issue variables. Unknown variables cause error.
- **Verification:** `cd go && go test ./internal/prompt/...`
- **Dependencies:** 1.2
- **Files:** `go/internal/prompt/prompt.go`, `go/internal/prompt/prompt_test.go`
- **Scope:** S
- **Approach:** Simple template engine using Go `text/template` with strict option (unknown vars error). Data model: `{{.Issue.Identifier}}`, `{{.Issue.Title}}`, `{{.Issue.Description}}`, `{{.Issue.Priority}}`, `{{.Issue.State}}`, `{{.Issue.URL}}`, `{{.Issue.Labels}}`, `{{.Issue.BlockedBy}}`, `{{.Attempt}}`. `Render(template string, issue Issue, attempt int) (string, error)`. Option `tmpl.Option("missingkey=error")` ensures strict variable checking.
- **Edge Cases:** Template with no variables → returns as-is. Issue with nil fields → template handles gracefully. Invalid template syntax → return parse error.
- **Rollback:** Delete prompt package

### 5.1 Codex JSON-RPC protocol types
- **Acceptance:** All JSON-RPC 2.0 message types (request, response, notification) serialize/deserialize correctly. Protocol constants defined.
- **Verification:** `cd go && go test ./internal/agent/codex/... -run TestProtocol`
- **Dependencies:** 1.2
- **Files:** `go/internal/agent/codex/protocol.go`, `go/internal/agent/codex/protocol_test.go`
- **Scope:** M
- **Approach:** Define types: `Request{Method, ID, Params}`, `Response{ID, Result, Error}`, `Notification{Method, Params}`, `InitializeParams{Capabilities, ClientInfo}`, `ThreadStartParams{ApprovalPolicy, Sandbox, CWD, DynamicTools}`, `TurnStartParams{ThreadID, Input, CWD, Title, ApprovalPolicy, SandboxPolicy}`, `ApprovalDecision{Decision}`, `ToolResult{Success, Output, ContentItems}`. Method name constants: MethodInitialize, MethodInitialized, MethodThreadStart, MethodTurnStart, MethodTurnCompleted, MethodTurnFailed, MethodTurnCancelled, MethodItemCommandApproval, MethodItemFileChangeApproval, MethodExecCommandApproval, MethodApplyPatchApproval, MethodItemToolCall, MethodItemToolRequestUserInput. Line-delimited JSON framing: `Encoder` writes JSON + `\n`, `Decoder` reads lines and parses JSON.
- **Edge Cases:** Messages with both `id` and `method` are requests. Messages with `id` but no `method` are responses. Messages with `method` but no `id` are notifications. Unknown fields in JSON → ignore gracefully.
- **Rollback:** Delete protocol.go

### 5.2 Codex adapter with approval handling
- **Acceptance:** Starts `codex app-server` subprocess, establishes JSON-RPC session, runs turns with auto-approval, handles dynamic tools. Works over local os/exec.
- **Verification:** `cd go && go test ./internal/agent/codex/... -run TestAdapter`
- **Dependencies:** 1.2, 5.1
- **Files:** `go/internal/agent/codex/adapter.go`, `go/internal/agent/codex/approval.go`, `go/internal/agent/codex/dynamictool.go`, `go/internal/agent/codex/adapter_test.go`
- **Scope:** L
- **Approach:** `CodexAdapter` implements Agent interface. `StartSession(ctx, opts)` spawns `bash -lc <command>`, sends initialize + initialized + thread/start, returns `CodexSession`. `CodexSession.RunTurn(ctx, prompt, opts)` sends turn/start, enters receive loop. Receive loop: read line → parse → if approval request → auto-approve → continue; if tool call → execute dynamic tool → send result → continue; if turn/completed → return success; if turn/failed/cancelled → return error; if input_required → return TurnInputRequired error. Dynamic tools: `linear_graphql` executes GraphQL against Linear client; `plane_rest` executes REST against Plane client. Test with a mock subprocess that sends canned JSON-RPC messages.
- **Edge Cases:** Subprocess exits mid-turn → return PortExit error. Turn timeout → kill subprocess, return timeout error. Read timeout (for initialize/thread/start responses) → return error. Stall timeout → return stall error. Malformed JSON → log and skip.
- **Rollback:** Delete adapter files, keep protocol

### 6.1 AgentRunner
- **Acceptance:** Creates workspace, runs hooks, builds prompt, starts agent session, runs turns until completion or max_turns, reports results back to orchestrator.
- **Verification:** `cd go && go test ./internal/agentrunner/...`
- **Dependencies:** 4.1, 4.2, 5.2
- **Files:** `go/internal/agentrunner/runner.go`, `go/internal/agentrunner/runner_test.go`
- **Scope:** M
- **Approach:** `Runner` struct holds dependencies: Workspace, PromptBuilder, Agent, Tracker, Config. `Run(ctx, issue Issue, attempt int, eventCh chan<- Event) error`. Steps: 1) Create/reuse workspace (before_run hook). 2) Render prompt. 3) Start agent session. 4) Loop: run turn, check issue state via tracker, if still active and turns < max_turns → continue with continuation prompt, else break. 5) Run after_run hook. 6) Close session. Events sent to eventCh for orchestrator tracking. Test with mock agent that returns canned TurnResults.
- **Edge Cases:** before_run hook fails → abort, return error. Prompt rendering fails → abort, return error. First turn vs continuation turns (different prompt content). Issue state changes to terminal mid-run → stop and return.
- **Rollback:** Delete agentrunner package

### 7.1 Orchestrator
- **Acceptance:** Polls tracker, dispatches issues up to concurrency limits, reconciles running issues, handles retries with exponential backoff, detects stalls, performs startup cleanup.
- **Verification:** `cd go && go test ./internal/orchestrator/...`
- **Dependencies:** 6.1, 2.2, 1.2
- **Files:** `go/internal/orchestrator/orchestrator.go`, `go/internal/orchestrator/state.go`, `go/internal/orchestrator/orchestrator_test.go`
- **Scope:** L
- **Approach:** `Orchestrator` struct: config, tracker, agentRunner, workspace, elector, state. `State` struct: running map[issueID]RunInfo, claimed set[issueID], retryAttempts map[issueID]RetryEntry, completed set[issueID]. `Run(ctx)` loop: campaign for leadership, then tick every pollInterval. `tick(ctx)`: 1) Reconcile running issues (check tracker state, detect stalls). 2) Validate dispatch preflight. 3) Fetch candidates. 4) Sort by priority/date/identifier. 5) Dispatch eligible (check concurrency limits). Each dispatch runs AgentRunner in a goroutine with event channel. On worker exit: normal → continuation retry (1s); failure → exponential backoff (10s * 2^attempt, capped at max_retry_backoff_ms). Startup: clean terminal workspaces, immediate first tick. Test with memory tracker + mock agent.
- **Edge Cases:** Concurrency: global limit and per-state limit. Priority sorting: null priority last. Blocked issues: Todo state with non-terminal blockers → skip. Retry timer fires but issue no longer active → release claim. Reconciliation failure → keep workers running.
- **Rollback:** Delete orchestrator package

### 8.1 Claude Code adapter
- **Acceptance:** Starts `claude -p --output-format stream-json` subprocess, parses NDJSON events, runs continuation turns. Works over local os/exec.
- **Verification:** `cd go && go test ./internal/agent/claude/...`
- **Dependencies:** 1.2, 5.1
- **Files:** `go/internal/agent/claude/adapter.go`, `go/internal/agent/claude/stream.go`, `go/internal/agent/claude/adapter_test.go`, `go/internal/agent/claude/stream_test.go`
- **Scope:** M
- **Approach:** `ClaudeAdapter` implements Agent interface. `StartSession(ctx, opts)` returns `ClaudeSession`. `ClaudeSession.RunTurn(ctx, prompt, opts)` spawns `claude -p <prompt> --output-format stream-json [--dangerously-skip-permissions | --allowedTools ...]`, reads stdout as NDJSON stream. `StreamParser` reads line-by-line, parses each as JSON, extracts type/content. Map stream events to Agent events: assistant message with tool_use → Notification, text → progress, end → TurnCompleted. Continuation turns: new subprocess invocation with continuation prompt including prior context. Session.Close() is no-op (no persistent subprocess). Test with mock subprocess outputting canned NDJSON.
- **Edge Cases:** Subprocess exits with non-zero → TurnFailed. Malformed JSON line → skip and log. Empty output → TurnCompleted with no usage. `--dangerously-skip-permissions` vs `--allowedTools` based on config.
- **Rollback:** Delete claude package

### 9.1 SSH client and remote execution
- **Acceptance:** SSH client can execute commands on remote host. Workspace operations (create, remove, hooks) work remotely. Agent can run over SSH.
- **Verification:** `cd go && go test ./internal/ssh/... ./internal/workspace/... -run TestSSH`
- **Dependencies:** 4.1, 5.2
- **Files:** `go/internal/ssh/client.go`, `go/internal/ssh/client_test.go`, `go/internal/workspace/remote.go`
- **Scope:** M
- **Approach:** `SSHClient` wraps `golang.org/x/crypto/ssh` package. `NewClient(host, config) (*Client, error)`. `RunCommand(ctx, command string) (stdout, stderr string, exitCode int, error)`. `StartSession(ctx, command string) (stdin io.WriteCloser, stdout io.Reader, error)` for persistent sessions (Codex). Config: read `~/.ssh/config`, support `SYMPHONY_SSH_CONFIG` env var override. `remote.go`: `CreateRemoteWorkspace`, `RemoveRemoteWorkspace`, `RunHookRemote` — all delegate to SSH command execution. Codex over SSH: persistent SSH session with stdin/stdout pipes for JSON-RPC. Claude over SSH: per-turn `ssh <host> "cd <workspace> && claude -p ..."`.
- **Edge Cases:** SSH connection refused → clear error. Host key verification → use known_hosts. Auth failure → clear error. Command timeout → kill remote process.
- **Rollback:** Delete ssh package and remote.go

### 10.1 HA Elector implementations
- **Acceptance:** LocalElector always returns IsLeader()=true, Campaign succeeds, Done() never closes. EtcdElector campaigns for leadership via etcd, detects leadership loss, provides LeaderAddr().
- **Verification:** `cd go && go test ./internal/ha/...`
- **Dependencies:** 1.2
- **Files:** `go/internal/ha/elector.go`, `go/internal/ha/local.go`, `go/internal/ha/etcd.go`, `go/internal/ha/elector_test.go`, `go/internal/ha/etcd_test.go`
- **Scope:** M
- **Approach:** `LocalElector`: Campaign() returns nil, IsLeader()=true, Resign() no-op, Done() returns channel that never closes, LeaderAddr() returns "localhost:<port>". `EtcdElector`: uses `go.etcd.io/etcd/client/v3/concurrency`. Campaign() calls `concurrency.NewSession` + `Election.Campaign`. IsLeader() checks if lease is still valid. Resign() calls `Election.Resign`. Done() returns session.Done() channel. LeaderAddr() reads the current leader value from etcd (stored as `advertise_addr`). On leadership loss, Done() channel closes, orchestrator should exit. Tests: LocalElector unit test, EtcdElector integration test with etcd test container (tagged `//go:build integration`).
- **Edge Cases:** etcd endpoints unreachable at startup → Campaign() returns error, daemon fails to start. Lease expires during operation → Done() fires. Multiple instances campaigning → only one wins.
- **Rollback:** Delete ha package

### 11.1 HTTP API server
- **Acceptance:** `/api/v1/state` returns JSON with running/retrying/totals/limits. `POST /api/v1/refresh` triggers immediate poll. `/healthz` returns 200. Standby redirects to leader.
- **Verification:** `cd go && go test ./internal/httpserver/...`
- **Dependencies:** 7.1, 10.1
- **Files:** `go/internal/httpserver/server.go`, `go/internal/httpserver/api.go`, `go/internal/httpserver/health.go`, `go/internal/httpserver/server_test.go`
- **Scope:** M
- **Approach:** `Server` struct: orchestrator reference, elector reference, config. `NewServer(cfg, orchestrator, elector) *Server`. `Start(ctx) error` starts `net/http` server on configured port (0 = ephemeral). Routes: `GET /api/v1/state` → read orchestrator snapshot, return JSON. `POST /api/v1/refresh` → call orchestrator.RequestRefresh(), return result. `GET /api/v1/:identifier` → read specific issue from snapshot. `GET /healthz` → return 200. All handlers check `elector.IsLeader()` first; if standby, redirect to `elector.LeaderAddr()`. Test with httptest and mock orchestrator.
- **Edge Cases:** Port 0 → bind to ephemeral, report actual port. Dashboard redirect preserves path. Refresh while already polling → coalesce.
- **Rollback:** Delete httpserver package

### 11.2 Dashboard with SSE
- **Acceptance:** Dashboard at `/` shows running agents, retry queue, token usage. SSE at `/api/v1/events` pushes real-time updates. Uses templ for HTML, htmx for interactions.
- **Verification:** `cd go && go test ./internal/httpserver/... -run TestDashboard`
- **Dependencies:** 11.1
- **Files:** `go/internal/httpserver/dashboard_templ.go`, `go/internal/httpserver/sse.go`, `go/internal/httpserver/server.go`, `go/internal/httpserver/server_test.go`
- **Scope:** M
- **Approach:** Install `github.com/a-h/templ` and `go:generate templ generate`. Create `dashboard_templ.go` with templ components: page layout, running agents table, retry queue table, token stats cards, rate limit display. Add `hx-ext="sse"` and `sse-connect="/api/v1/events"` to body for auto-updates. SSE endpoint: goroutine subscribes to orchestrator state changes, writes `data: {json}\n\n` on each change. Static assets (htmx.min.js, sse.js) embedded via `go:embed`. Dashboard auto-polls as fallback if SSE disconnects.
- **Edge Cases:** SSE client disconnects → clean up goroutine. Multiple SSE clients → fan-out. Proxy buffering → set `X-Accel-Buffering: no` header.
- **Rollback:** Delete dashboard and SSE files

### 12.1 CLI, signals, and graceful shutdown
- **Acceptance:** CLI accepts `--workflow`, `--port`, `--i-understand-the-risks` flags. SIGTERM/SIGINT triggers graceful shutdown. Logging format switches with `SYMPHONY_LOG_FORMAT`. All log statements include `issue_id`, `issue_identifier`, `session_id` where applicable via slog context.
- **Verification:** `cd go && go build -o symphony ./cmd/symphony && ./symphony --help`
- **Dependencies:** 7.1, 10.1, 11.1
- **Files:** `go/cmd/symphony/main.go`
- **Scope:** M
- **Approach:** Parse flags with `flag` package. `--workflow` sets WORKFLOW.md path (default: `WORKFLOW.md` in cwd). `--port` overrides server.port config. `--i-understand-the-risks` required acknowledgement (exit if missing). Initialize logging: if `SYMPHONY_LOG_FORMAT=json` → `slog.NewJSONHandler(os.Stdout)`, else `slog.NewTextHandler(os.Stdout)`. Structured context: create a helper `logger.WithIssue(issue)` and `logger.WithSession(sessionID)` that return `slog.Logger` with pre-set attributes. All packages use these helpers when logging issue/session-specific operations. Wire all components: load config → create elector → create tracker adapter → create agent adapter → create workspace → create agent runner → create orchestrator → create HTTP server → start. Signal handling: `signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)`. On context cancellation: orchestrator stops dispatching, agents receive shutdown, HTTP server shuts down with 10s timeout.
- **Edge Cases:** Missing `--i-understand-the-risks` → print warning and exit. Invalid workflow path → clear error. Config validation failure → exit with message.
- **Rollback:** Revert main.go to stub

### 12.2 Documentation and build artifacts
- **Acceptance:** README.md documents the Go implementation. SPEC-GO.md documents extensions. Makefile provides build/test/lint targets. Copy of SPEC.md in go/docs/.
- **Verification:** `cd go && make all`
- **Dependencies:** 12.1
- **Files:** `go/README.md`, `go/docs/SPEC-GO.md`, `go/docs/SPEC.md`, `go/Makefile`
- **Scope:** S
- **Approach:** README: project overview, quick start, configuration reference, deployment modes, development commands. SPEC-GO.md: document all 12 SPEC-GO extensions from spec.md. Copy root SPEC.md to go/docs/SPEC.md (immutable reference). Makefile targets: `build`, `test`, `coverage`, `lint`, `fmt`, `generate` (templ), `all` (fmt + lint + test + build).
- **Edge Cases:** None
- **Rollback:** Delete docs and Makefile

## Parallelization Classification

| Category | Tasks | Strategy |
|----------|-------|----------|
| Sequential | 1.1 → 1.2 → 1.3 → 2.1 → 2.2 → 4.1 → 4.2 → 5.1 → 5.2 → 6.1 → 7.1 → 11.1 → 11.2 → 12.1 → 12.2 | Foundation must be built in order |
| Parallel with 3.2 | 3.3 (Plane adapter) | After Tracker interface defined (1.2), Plane can be built in parallel with Linear |
| Parallel with 5.2 | 8.1 (Claude adapter) | After Agent interface defined (1.2), Claude can be built in parallel with Codex |
| Parallel with 7.1 | 9.1 (SSH), 10.1 (HA) | After core interfaces, SSH and HA are independent |

## Risk/Mitigation Table

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent interface doesn't fit both Codex and Claude Code | High | Validate with both adapters in parallel (tasks 5.2 and 8.1). If interface gaps found, adjust before building orchestrator. |
| Codex JSON-RPC protocol differs from documented | High | Test with real `codex app-server` early. Protocol types in separate file for easy adjustment. |
| Plane REST API undocumented or unstable | Medium | Build adapter against httptest mocks. Tag integration tests separately. |
| SSH stdio tunneling unreliable for long-running Codex sessions | Medium | Test with network simulation. Add reconnection logic if needed. |
| templ + htmx learning curve | Low | Dashboard is secondary. Core daemon works without it. |
| etcd client dependency bloat | Low | Only imported when ha.enabled=true. ~5MB acceptable for server binary. |
| E2E test with real agents is complex | Medium | Use mock agent adapter for most tests. Real agent tests tagged separately. |

## Self-Audit Checklist

- [x] Every spec requirement maps to at least one task
- [x] No task depends on a later task (no circular dependencies)
- [x] Every task has acceptance criteria that are independently verifiable
- [x] No placeholders (TBD, TODO, "implement later") in any task detail
- [x] File paths are specific and accurate for this codebase
- [x] Rollback strategy exists for every task

## Explicit Handoff Statement

**This plan is ready for implementation by subagent or inline execution. The implementing agent should:**
1. Read this plan.md first for context and approach
2. Update task.md checkboxes as work progresses
3. Run verification commands listed in each task after completion
4. Flag any blocking issues immediately rather than working around them

## Open Questions
- Should `agent.kind` be auto-detected from `codex.command` presence, or always explicit? (Recommendation: always explicit for clarity)
- Should Plane adapter support Plane Self-Hosted from day one? (Recommendation: yes, just a config field)
- Minimum Claude Code version for `--output-format stream-json`? (Needs verification)
