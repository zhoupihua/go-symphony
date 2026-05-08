# Proposal: Go Symphony — Multi-Tracker, Multi-Agent Daemon

## Why

How might we build a production-grade Go daemon that orchestrates coding agents across multiple issue trackers (Linear, Plane, and future trackers) and multiple coding agents (Codex, Claude Code, and future agents), while conforming to SPEC.md and supporting local, cloud, and distributed deployment?

## What Changes

Build the complete Go implementation of Symphony from scratch in `go/`. The design uses **configuration-driven composition** (V5):

- **Tracker** and **Agent** are Go interfaces — adapters are selected by WORKFLOW.md config (`tracker.kind`, `agent.kind`)
- **Elector** interface abstracts leadership — `LocalElector` for single instance (zero deps), `EtcdElector` for HA (etcd)
- **Execution mode** is orthogonal to agent type — local `os/exec` or SSH remote, selected by `worker.ssh_hosts`
- **Dashboard** built with templ + htmx + SSE — real-time Web UI, single binary, no JS framework
- Adding a new tracker or agent requires only implementing the interface and registering — zero changes to orchestrator

Deployment modes from a single binary:

| Mode | ha.enabled | worker.ssh_hosts | etcd |
|------|-----------|-----------------|------|
| Local single | false | empty | no |
| Cloud single | false | empty | no |
| Cloud + SSH workers | false | configured | no |
| Cloud HA | true | empty | yes |
| Cloud HA + SSH | true | configured | yes |

## Goals

- Full SPEC.md core conformance (all 17 requirements in Section 18.1)
- Pluggable Tracker interface: Linear + Plane from day one
- Pluggable Agent interface: Codex + Claude Code from day one
- Local subprocess execution and SSH remote execution from day one
- Single-instance (no etcd) and multi-instance HA (etcd) from a single binary
- Web UI dashboard with real-time SSE updates; standby redirects to leader
- Cloud and local deployment; container-friendly logging
- Pure Go: no frameworks, stdlib first, context propagation, error wrapping

## Non-Goals

- **Persistent database** — SPEC.md requires in-memory state; restart recovery via tracker/filesystem
- **Active-active horizontal scaling** — only active-standby HA
- **SPEC.md modification** — SPEC.md is immutable; Go-specific extensions go in `go/docs/SPEC-GO.md`
- **First-class tracker write APIs** — SPEC.md §11.5 excludes this; agent tools handle writes
- **API-based agent adapters (Phase 1)** — Devin/OpenHands-style HTTP API agents deferred to Phase 2; interface accommodates them
- **Worker pull-based registration** — NAT/防火墙穿透方案 deferred; Phase 1 uses SSH (Symphony → Worker)
- **Single-instance multi-project** — one instance per project; multi-project via multiple instances with different WORKFLOW.md. Future: `projects` array in WORKFLOW.md for single-instance multi-project support.

## Key Assumptions

- [ ] Plane's REST API is stable enough to build a production adapter
- [ ] Claude Code's `claude -p --output-format stream-json` mode is sufficient for agent integration
- [ ] The Elixir reference implementation behavior is the ground truth for Linear + Codex
- [ ] Go interfaces can abstract Codex (persistent JSON-RPC session) and Claude Code (per-turn subprocess)
- [ ] SSH stdio tunneling works reliably for both Codex and Claude Code remote execution
- [ ] SSE + htmx is sufficient for dashboard real-time updates
- [ ] etcd client v3 is stable enough for leader election in HA mode

## Open Questions

- Should `agent.kind` be auto-detected from `codex.command` presence, or always explicit?
- Should the Plane adapter support Plane Self-Hosted (custom base URL) from day one?
- What is the minimum Claude Code version that supports `--output-format stream-json`?
