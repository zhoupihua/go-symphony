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
├── go/                  # Go-zero implementation (in progress)
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

Go-zero implementation of Symphony, referencing the Elixir implementation and conforming to SPEC.md.

### go-zero AI Context

Go-zero AI tools are installed as submodules:
- `.claude/ai-context/` — workflow-level context (instructions, patterns, tools, workflows)
- `.claude/skills/zero-skills/` — knowledge-level context (detailed patterns, references, troubleshooting)

When working in `go/`, follow the spec-first development flow defined in `.claude/ai-context/00-instructions.md`:
1. Write `.api` or `.proto` spec first
2. Generate code with `goctl`
3. Fill in business logic
4. Run post-generation steps: `go mod tidy` → verify imports → `go build ./...`

### go-zero Conventions

- Context first: `func(ctx context.Context, req *types.Request)`
- Errors: `errorx.NewCodeError(code, msg)`
- Config: `json:",default=value"`
- Validation: `validate:"required"`
- Always generate README.md for new services

## Architecture

Symphony has six abstraction layers (per SPEC.md §3.2):

| Layer | Responsibility |
|-------|---------------|
| Policy | Repo-defined workflow (`WORKFLOW.md` prompt + rules) |
| Configuration | Typed getters, defaults, env resolution |
| Coordination | Polling loop, concurrency, retries, reconciliation |
| Execution | Workspace lifecycle, agent subprocess management |
| Integration | Issue tracker API adapter (Linear) |
| Observability | Structured logs, status dashboard |

The Elixir implementation maps these to:
- Policy → `Workflow`, `WorkflowStore`
- Configuration → `Config`, `Config.Schema`
- Coordination → `Orchestrator` (GenServer)
- Execution → `Workspace`, `AgentRunner`, `Codex.AppServer`
- Integration → `Tracker` (behaviour), `Linear.Adapter`
- Observability → `LogFile`, `StatusDashboard`, `HttpServer`

The Go implementation should follow the same layering. Each layer should be a separate package with clear interfaces.

## Cross-Implementation Rules

- Changes must not conflict with SPEC.md; if implementation alters intended behavior, update the spec in the same change
- Both implementations should produce the same observable behavior for the same WORKFLOW.md input
- Elixir is the reference implementation; when in doubt about spec interpretation, check `elixir/lib/`
