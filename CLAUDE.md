# Symphony

Symphony is a long-running automation service that polls issue trackers for work, creates isolated workspaces per issue, and runs coding agents inside those workspaces.

The authoritative service specification is [SPEC.md](SPEC.md). All implementations must conform to it.

## Project Structure

```
symphony/
├── SPEC.md              # Language-agnostic service specification (source of truth)
├── elixir/              # Elixir/OTP reference implementation
│   ├── lib/             # Application code
│   ├── test/            # ExUnit tests
│   ├── WORKFLOW.md      # In-repo workflow contract
│   └── AGENTS.md        # Elixir-specific AI agent instructions
├── go/                  # Go native implementation (in progress)
│   └── README.md
└── docs/                # Documentation
```

## Elixir Implementation

See [elixir/AGENTS.md](elixir/AGENTS.md) for Elixir-specific conventions.

Key points:
- Elixir 1.19.x (OTP 28), managed via `mise`
- Quality gate: `make all` (format, lint, coverage, dialyzer)
- Public functions (`def`) in `lib/` must have `@spec`
- Config via `WORKFLOW.md` front matter, accessed through `SymphonyElixir.Config`
- Workspace safety: never run agent commands in the source repo; workspaces must stay under configured root

## Go Implementation

Native Go implementation of Symphony as a long-running daemon, referencing the Elixir implementation and conforming to SPEC.md.

### Architecture

Configuration-driven composition (V5):
- Core interfaces: `Tracker`, `Agent`/`Session`, `Elector`
- Adapters selected by WORKFLOW.md config (`tracker.kind`, `agent.kind`)
- Execution mode: local `os/exec` or SSH remote, selected by `worker.ssh_hosts`
- HA: `LocalElector` (single instance, no deps) or `EtcdElector` (etcd-based leader election)

### Six Abstraction Layers

| Layer | Go Package | Responsibility |
|-------|-----------|---------------|
| Policy | `workflow`, `workflowstore` | WORKFLOW.md loading, file watching, prompt template |
| Configuration | `config` | Typed getters, defaults, $VAR resolution |
| Coordination | `orchestrator` | Poll loop, dispatch, reconcile, retry |
| Execution | `workspace`, `agentrunner` | Workspace lifecycle, agent subprocess management |
| Integration | `tracker` (interface), `linear`, `plane` | Issue tracker API adapters |
| Observability | `httpserver`, `ha` | Dashboard, SSE, health, leader election |

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

| Mode | ha.enabled | worker.ssh_hosts | etcd |
|------|-----------|-----------------|------|
| Local single | false | empty | no |
| Cloud single | false | empty | no |
| Cloud + SSH workers | false | configured | no |
| Cloud HA | true | empty | yes |
| Cloud HA + SSH | true | configured | yes |

### Dashboard

- Web UI: templ + htmx + SSE (real-time updates, no JS framework)
- Leader: serves dashboard with live orchestrator state
- Standby: redirects to leader's dashboard
- API: `/api/v1/state`, `/api/v1/refresh`, `/api/v1/events` (SSE), `/healthz`

## Cross-Implementation Rules

- Changes must not conflict with SPEC.md; if implementation alters intended behavior, update the spec in the same change
- Both implementations should produce the same observable behavior for the same WORKFLOW.md input
- Elixir is the reference implementation; when in doubt about spec interpretation, check `elixir/lib/`
