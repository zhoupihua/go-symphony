# Tasks: Go Symphony

## 1. Foundation
- [ ] 1.1 Project scaffold and go.mod
- [ ] 1.2 Core data types and interfaces (Tracker, Agent/Session, Elector, Issue, Events)
- [ ] 1.3 Workflow loader (YAML front matter + prompt body parser)

## 2. Configuration
- [ ] 2.1a Config schema struct definitions with defaults
- [ ] 2.1b Config $VAR resolution and validation
- [ ] 2.2 WorkflowStore with file watcher

## 3. Tracker Adapters
- [ ] 3.1 Tracker memory adapter (for testing)
- [ ] 3.2 Linear tracker adapter (GraphQL + pagination + normalization)
- [ ] 3.3 Plane tracker adapter (REST + pagination + state group mapping)

## 4. Workspace & Prompt
- [ ] 4.1 Workspace and path safety (sanitize, canonicalize, hooks, timeout)
- [ ] 4.2 Prompt builder (template rendering with strict variable checking)

## 5. Codex Agent
- [ ] 5.1 Codex JSON-RPC protocol types (request/response/notification framing)
- [ ] 5.2 Codex adapter with approval handling and dynamic tools

## 6. Agent Runner
- [ ] 6.1 AgentRunner (workspace + prompt + agent session orchestration)

## 7. Orchestrator
- [ ] 7.1 Orchestrator (poll loop, dispatch, reconcile, retry, stall detection)

## 8. Claude Code Agent
- [ ] 8.1 Claude Code adapter (one-shot subprocess + NDJSON stream parsing)

## 9. SSH Remote Execution
- [ ] 9.1 SSH client and remote workspace/agent execution

## 10. High Availability
- [ ] 10.1 HA Elector implementations (LocalElector + EtcdElector)

## 11. Observability
- [ ] 11.1 HTTP API server (state, refresh, healthz, standby redirect)
- [ ] 11.2 Dashboard with SSE (templ + htmx + real-time updates)

## 12. CLI & Deployment
- [ ] 12.1 CLI flags, signal handling, logging, graceful shutdown
- [ ] 12.2 Documentation (README, SPEC-GO, Makefile)
