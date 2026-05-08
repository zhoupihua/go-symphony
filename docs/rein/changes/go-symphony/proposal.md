# Proposal: Go Symphony — Multi-Tracker, Multi-Agent Daemon

## Why

How might we build a production-grade Go daemon that orchestrates coding agents across multiple issue trackers (Linear, Plane, and future trackers) and multiple coding agents (Codex, Claude Code, and future agents), while conforming to SPEC.md and supporting both single-instance and multi-instance HA deployment?

## What Changes

Build the complete Go implementation of Symphony from scratch in `go/`. Unlike the Elixir reference (which only supports Linear + Codex), this implementation uses a **configuration-driven composition** architecture (V5) where:

- `Tracker` and `Agent` are Go interfaces, not coupled to any specific implementation
- `Elector` interface abstracts leadership — `LocalElector` for single-instance, `EtcdElector` for HA
- WORKFLOW.md's `tracker.kind`, `agent.kind`, and `ha.enabled` select implementations at startup
- Adapters live in independent packages, importable via dependency injection
- The orchestrator depends only on interfaces, never on concrete adapters

This means adding a new tracker (e.g., Jira, GitHub Issues) or a new coding agent (e.g., Devin, OpenHands, Aider) requires only implementing the interface and registering it — zero changes to the orchestrator or other adapters.

The daemon runs as a single Go binary. Single-instance mode has zero external dependencies (no etcd). Multi-instance HA mode uses etcd for leader election. Both modes include a Web UI dashboard (templ + htmx + SSE) for real-time observability.

## Goals

- Full SPEC.md core conformance (all 17 requirements in Section 18.1)
- Pluggable Tracker interface with Linear and Plane adapters from day one
- Pluggable Agent interface with Codex (JSON-RPC app-server) and Claude Code (one-shot CLI) adapters from day one
- Single-instance and multi-instance HA deployment from a single binary
- Single-instance: zero external dependencies (no etcd required)
- Multi-instance: etcd-based leader election with automatic failover
- Web UI dashboard with real-time updates (templ + htmx + SSE)
- Standby instances redirect dashboard to leader
- Cloud and local deployment
- Structured logging via `log/slog` (text for local, JSON for cloud)
- All adapters independently testable with interface mocks
- Pure Go: no frameworks, stdlib first, context propagation, error wrapping

## Non-Goals

- **Persistent database** — SPEC.md requires in-memory state only; restart recovery via tracker/filesystem
- **go-zero framework** — native Go with selective etcd client dependency only
- **SPEC.md modification** — SPEC.md is the immutable source of truth; Go-specific extensions go in `go/docs/SPEC-GO.md`
- **SSH worker extension (Phase 1)** — deferred to Phase 2; the interface will accommodate it
- **First-class tracker write APIs** — SPEC.md §11.5 explicitly excludes this; agent tools handle writes
- **Active-active horizontal scaling** — only active-standby HA; each instance handles all configured trackers/projects
- **Phoenix LiveView equivalent** — we use SSE + htmx, not WebSocket + server-side rendering

## Key Assumptions

- [ ] Plane's REST API is stable enough to build a production adapter (validate by reading Plane API docs and testing endpoints)
- [ ] Claude Code's `claude -p --output-format stream-json` mode is sufficient for agent integration (validate by running a test invocation)
- [ ] The Elixir reference implementation behavior is the ground truth for Linear + Codex (validate by reading Elixir source)
- [ ] Go interfaces can abstract away the fundamental protocol differences between Codex (bidirectional JSON-RPC session) and Claude Code (one-shot subprocess per turn) (validate by designing the interface and checking both adapters satisfy it)
- [ ] SSE + htmx is sufficient for dashboard real-time updates (validate by building a prototype)
- [ ] etcd client v3 is stable and lightweight enough for leader election without pulling in heavy dependencies (validate by checking go.mod size)

## Open Questions

- Should `agent.kind` be a new WORKFLOW.md front-matter key, or should we auto-detect from `codex.command`?
- Should the Plane adapter support Plane Self-Hosted (different base URL) from day one, or just plane.so?
- What is the minimum Claude Code version that supports `--output-format stream-json`?
