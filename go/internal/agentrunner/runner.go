package agentrunner

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/prompt"
	"github.com/ainative/go-symphony/internal/tracker"
	"github.com/ainative/go-symphony/internal/workspace"
)

// RunResult holds the outcome of a full agent run for an issue.
type RunResult struct {
	Completed   bool
	TotalTurns  int
	TotalUsage  agent.UsageReport
	FinalOutput string
}

// Runner orchestrates the full agent lifecycle for a single issue:
// create workspace, run hooks, render prompt, start session, run turns, clean up.
type Runner struct {
	Agent    agent.Agent
	Tracker  tracker.Tracker
	Cfg      config.Schema
	Tmpl     string // prompt template from workflow
}

// Run executes the full agent lifecycle for the given issue.
// It sends events to eventCh for observability and returns the final result.
func (r *Runner) Run(ctx context.Context, issue tracker.Issue, attempt int, eventCh chan<- agent.Event) (RunResult, error) {
	// 1. Create workspace.
	hookTimeout := hookTimeout(r.Cfg)
	wsPath, created, err := workspace.Create(ctx, r.Cfg.Workspace.Root, issue.Identifier, r.Cfg.Hooks.AfterCreate, hookTimeout)
	if err != nil {
		return RunResult{}, fmt.Errorf("create workspace: %w", err)
	}
	slog.Info("workspace ready", "path", wsPath, "created", created, "issue", issue.Identifier)

	// 2. Run before_run hook.
	if r.Cfg.Hooks.BeforeRun != "" {
		if err := workspace.RunHook(ctx, r.Cfg.Hooks.BeforeRun, wsPath, hookTimeout); err != nil {
			return RunResult{}, fmt.Errorf("before_run hook failed: %w", err)
		}
	}

	// 3. Render prompt.
	promptText, err := prompt.Render(r.Tmpl, issue, attempt)
	if err != nil {
		return RunResult{}, fmt.Errorf("render prompt: %w", err)
	}

	// 4. Start agent session.
	sess, err := r.Agent.StartSession(ctx, agent.SessionOptions{
		WorkspacePath: wsPath,
		Tracker:       r.Tracker,
		Issue:         issue,
		Config:        agentConfigMap(r.Cfg),
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("start session: %w", err)
	}
	defer sess.Close()

	// 5. Turn loop.
	maxTurns := r.Cfg.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	var result RunResult
	currentPrompt := promptText

	for result.TotalTurns < maxTurns {
		// Check context.
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("run cancelled: %w", ctx.Err())
		default:
		}

		turnResult, err := sess.RunTurn(ctx, currentPrompt, agent.TurnOptions{
			MaxTurns:       maxTurns - result.TotalTurns,
			ApprovalPolicy: r.Cfg.Agent.Codex.ApprovalPolicy,
		})
		if err != nil {
			sendEvent(eventCh, agent.EventTurnFailed, issue.ID, err.Error(), nil)
			return result, fmt.Errorf("turn %d failed: %w", result.TotalTurns+1, err)
		}

		result.TotalTurns++
		result.TotalUsage.InputTokens += turnResult.Usage.InputTokens
		result.TotalUsage.OutputTokens += turnResult.Usage.OutputTokens
		result.TotalUsage.TotalTokens += turnResult.Usage.TotalTokens

		sendEvent(eventCh, agent.EventTurnCompleted, issue.ID, turnResult.Output, &turnResult.Usage)

		if turnResult.Completed {
			result.Completed = true
			result.FinalOutput = turnResult.Output
			break
		}

		// Check if issue is still in an active state before continuing.
		if r.Tracker != nil {
			stillActive, err := r.issueStillActive(ctx, issue)
			if err != nil {
				slog.Warn("failed to check issue state, continuing", "error", err, "issue", issue.ID)
			} else if !stillActive {
				slog.Info("issue left active state, stopping", "issue", issue.ID)
				sendEvent(eventCh, agent.EventTurnCompleted, issue.ID, "issue left active state", nil)
				break
			}
		}

		// Build continuation prompt for next turn.
		currentPrompt = fmt.Sprintf("Continue working on the issue. Turn %d of %d.", result.TotalTurns+1, maxTurns)
	}

	// 6. Run after_run hook (failure is logged, not blocking).
	if r.Cfg.Hooks.AfterRun != "" {
		if err := workspace.RunHook(ctx, r.Cfg.Hooks.AfterRun, wsPath, hookTimeout); err != nil {
			slog.Warn("after_run hook failed", "error", err, "path", wsPath)
		}
	}

	return result, nil
}

// issueStillActive checks whether the issue is still in an active state.
func (r *Runner) issueStillActive(ctx context.Context, issue tracker.Issue) (bool, error) {
	activeStates := r.Cfg.Tracker.ActiveStates
	if len(activeStates) == 0 {
		return true, nil // no active states configured means always active
	}

	issues, err := r.Tracker.FetchIssueStatesByIDs(ctx, []string{issue.ID})
	if err != nil {
		return false, err
	}
	if len(issues) == 0 {
		return false, nil
	}

	return slices.Contains(activeStates, issues[0].State), nil
}

// hookTimeout returns the configured hook timeout or a sensible default.
func hookTimeout(cfg config.Schema) time.Duration {
	if cfg.Hooks.TimeoutMS > 0 {
		return time.Duration(cfg.Hooks.TimeoutMS) * time.Millisecond
	}
	return 5 * time.Minute
}

// agentConfigMap converts the relevant agent config into a map for SessionOptions.Config.
func agentConfigMap(cfg config.Schema) map[string]any {
	switch cfg.Agent.Kind {
	case "codex":
		m := map[string]any{
			"command":         cfg.Agent.Codex.Command,
			"approval_policy": cfg.Agent.Codex.ApprovalPolicy,
		}
		if cfg.Agent.Codex.TurnTimeoutMS > 0 {
			m["turn_timeout_ms"] = float64(cfg.Agent.Codex.TurnTimeoutMS)
		}
		if cfg.Agent.Codex.ReadTimeoutMS > 0 {
			m["read_timeout_ms"] = float64(cfg.Agent.Codex.ReadTimeoutMS)
		}
		if cfg.Agent.Codex.StallTimeoutMS > 0 {
			m["stall_timeout_ms"] = float64(cfg.Agent.Codex.StallTimeoutMS)
		}
		if cfg.Agent.Codex.ThreadSandbox != "" {
			m["thread_sandbox"] = cfg.Agent.Codex.ThreadSandbox
		}
		return m
	case "claude":
		m := map[string]any{
			"command": cfg.Agent.Claude.Command,
		}
		if cfg.Agent.Claude.PermissionMode != "" {
			m["permission_mode"] = cfg.Agent.Claude.PermissionMode
		}
		if len(cfg.Agent.Claude.AllowedTools) > 0 {
			m["allowed_tools"] = cfg.Agent.Claude.AllowedTools
		}
		if cfg.Agent.Claude.MaxTurns > 0 {
			m["max_turns"] = cfg.Agent.Claude.MaxTurns
		}
		return m
	default:
		return map[string]any{
			"command": cfg.Agent.Codex.Command,
		}
	}
}

// sendEvent sends an event to the channel without blocking.
func sendEvent(ch chan<- agent.Event, typ agent.EventType, issueID, msg string, usage *agent.UsageReport) {
	if ch == nil {
		return
	}
	select {
	case ch <- agent.Event{
		Type:      typ,
		IssueID:   issueID,
		Message:   msg,
		Usage:     usage,
		Timestamp: time.Now(),
	}:
	default:
		// Drop event if channel is full.
	}
}
