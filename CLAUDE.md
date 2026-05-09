# Symphony

Symphony is a long-running automation service that polls issue trackers for work, creates isolated workspaces per issue, and runs coding agents inside those workspaces.

The authoritative service specification is [SPEC.md](reference/symphony/SPEC.md). All implementations must conform to it.

## Project Structure

```
symphony/
├── cmd/symphony/         # CLI entry point
├── internal/             # Core packages (6 layers)
├── docs/                 # Change documentation
├── reference/symphony/      # Reference implementation (submodule)
│   ├── SPEC.md           # Language-agnostic service specification (source of truth)
│   └── elixir/           # Elixir/OTP reference implementation
├── SPEC-GO.md            # Go-specific spec extensions
├── Makefile              # Build, test, lint targets
├── README.md             # Project documentation
└── CLAUDE.md             # AI agent instructions
```

## Reference Implementation

The original openai/symphony repository is available as a submodule at `reference/symphony/`.

- Elixir conventions: [reference/symphony/elixir/AGENTS.md](reference/symphony/elixir/AGENTS.md)
- When in doubt about spec interpretation, check `reference/symphony/elixir/lib/`

## Go Implementation

Native Go implementation of Symphony as a long-running daemon, conforming to SPEC.md.

### Architecture

Configuration-driven composition:
- Core interfaces: `Tracker`, `Agent`/`Session`, `Elector`
- Adapters selected by WORKFLOW.md config (`tracker.kind`, `agent.kind`)
- Execution mode: local `os/exec` or SSH remote, selected by `worker.ssh_hosts`
- HA: `LocalElector` (single instance, no deps) or `RaftElector` (embedded Raft consensus)

### Six Abstraction Layers

| Layer | Go Package | Responsibility |
|-------|-----------|---------------|
| Policy | `workflow` | WORKFLOW.md loading, file watching, prompt template |
| Configuration | `config` | Struct with YAML tags, defaults, $VAR resolution |
| Coordination | `orchestrator` | Poll loop, dispatch, reconcile, retry |
| Execution | `workspace`, `agentrunner` | Workspace lifecycle, agent subprocess management |
| Integration | `tracker` (interface), `linear`, `plane` | Issue tracker API adapters |
| Observability | `httpserver`, `ha` | Dashboard, SSE, health, leader election, state replication |

### Go Conventions

- Standard library first; avoid heavy frameworks
- Context propagation: `func(ctx context.Context, ...)`
- Errors: `fmt.Errorf("...: %w", err)` with error wrapping
- Config: struct with env tags, sensible defaults
- Logging: structured logging via `log/slog`
- Long-running loops use `context.Context` for cancellation
- Adapters register explicitly (no `init()`)

### Supported Trackers

| Tracker | `tracker.kind` | API Style |
|---------|---------------|-----------|
| Linear | `"linear"` | GraphQL |
| Plane | `"plane"` | REST |

### Supported Agents

| Agent | `agent.kind` | Protocol | SSH Support |
|-------|-------------|----------|-------------|
| Codex | `"codex"` | JSON-RPC 2.0 over stdio (persistent session) | Persistent SSH session |
| Claude Code | `"claude"` | NDJSON stream over stdout (per-turn subprocess) | Per-turn SSH command |

### Deployment Modes

| Mode | ha.enabled | worker.ssh_hosts |
|------|-----------|-----------------|
| Local single | false | empty |
| Cloud single | false | empty |
| Cloud + SSH workers | false | configured |
| Cloud HA | true | empty |
| Cloud HA + SSH | true | configured |

### Dashboard

- Web UI: templ + htmx + SSE (real-time updates, no JS framework)
- Leader: serves dashboard with live orchestrator state
- Standby: redirects to leader's dashboard
- API: `/api/v1/state`, `/api/v1/refresh`, `/api/v1/events` (SSE), `/api/v1/cluster`, `/healthz`

## Cross-Implementation Rules

- Changes must not conflict with SPEC.md; if implementation alters intended behavior, update the spec in the same change
- Both implementations should produce the same observable behavior for the same WORKFLOW.md input
- Elixir is the reference implementation; when in doubt about spec interpretation, check `reference/symphony/elixir/lib/`
