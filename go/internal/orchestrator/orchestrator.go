package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/agentrunner"
	"github.com/ainative/go-symphony/internal/config"
	"github.com/ainative/go-symphony/internal/ha"
	"github.com/ainative/go-symphony/internal/sshclient"
	"github.com/ainative/go-symphony/internal/tracker"
	"github.com/ainative/go-symphony/internal/workspace"
)

const (
	continuationRetryDelay = 1 * time.Second
	failureRetryBase       = 10 * time.Second
)

// Orchestrator is the main coordination loop. It polls the tracker for
// candidate issues, dispatches them to agent runners up to concurrency limits,
// and reconciles running issues for stall detection and state changes.
type Orchestrator struct {
	Tracker      tracker.Tracker
	Agent        agent.Agent
	Elector      ha.Elector
	Cfg          func() config.Schema // called each tick for hot-reload support
	Tmpl         func() string        // called each tick for hot-reload support
	State        *State
	nextHostIdx  int // round-robin index for SSH host selection
}

// New creates a new Orchestrator.
func New(tr tracker.Tracker, ag agent.Agent, el ha.Elector, cfgFn func() config.Schema, tmplFn func() string) *Orchestrator {
	return &Orchestrator{
		Tracker: tr,
		Agent:   ag,
		Elector: el,
		Cfg:     cfgFn,
		Tmpl:    tmplFn,
		State:   NewState(),
	}
}

// selectWorkerHost picks a host from the SSH host pool using round-robin.
// Returns empty string for local execution.
func (o *Orchestrator) selectWorkerHost(cfg config.Schema) string {
	hosts := cfg.Worker.SSHHosts
	if len(hosts) == 0 {
		return ""
	}
	host := hosts[o.nextHostIdx%len(hosts)]
	o.nextHostIdx++
	return host
}

// Run starts the orchestrator loop. It blocks until the context is cancelled.
func (o *Orchestrator) Run(ctx context.Context) error {
	cfg := o.Cfg()

	// Startup: clean up workspaces for terminal issues.
	o.startupCleanup(ctx, cfg)

	// Immediate first tick.
	o.tick(ctx)

	interval := cfgPollInterval(cfg)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
			// Re-read config for hot-reload.
			cfg = o.Cfg()
			interval = cfgPollInterval(cfg)

			// If HA is enabled, only tick when leader.
			if o.Elector != nil && !o.Elector.IsLeader() {
				continue
			}

			o.tick(ctx)
		}
	}
}

// ForceRefresh triggers an immediate poll cycle.
func (o *Orchestrator) ForceRefresh(ctx context.Context) {
	o.tick(ctx)
}

// tick performs one poll cycle: reconcile running issues, process retries,
// fetch candidates, dispatch eligible.
func (o *Orchestrator) tick(ctx context.Context) {
	cfg := o.Cfg()

	// 1. Reconcile running issues (stall detection, state changes).
	o.reconcileRunning(ctx, cfg)

	// 2. Process due retries.
	o.processRetries(ctx, cfg)

	// 3. Fetch candidate issues.
	candidates, err := o.Tracker.FetchCandidateIssues(ctx)
	if err != nil {
		slog.Error("fetch candidate issues", "error", err)
		return
	}

	// 4. Sort and dispatch eligible.
	o.dispatch(ctx, cfg, candidates)
}

// reconcileRunning checks all running issues for stalls and state changes.
func (o *Orchestrator) reconcileRunning(ctx context.Context, cfg config.Schema) {
	running := o.State.Running()
	stallTimeout := stallTimeout(cfg)

	for issueID, info := range running {
		// Stall detection.
		if stallTimeout > 0 {
			elapsed := time.Since(info.LastActivity)
			if elapsed > stallTimeout {
				slog.Warn("stall detected, terminating agent", "issue", issueID, "elapsed", elapsed)
				o.State.RemoveRunning(issueID)
				o.State.ReleaseClaim(issueID)
				o.addRuntime(info)
				o.scheduleRetry(issueID, info, false, cfg)
				continue
			}
		}

		// Check if issue has moved to a terminal state.
		if o.Tracker != nil {
			issues, err := o.Tracker.FetchIssueStatesByIDs(ctx, []string{issueID})
			if err != nil {
				slog.Warn("reconcile: failed to check issue state", "issue", issueID, "error", err)
				continue
			}
			if len(issues) > 0 && isTerminalState(issues[0].State, cfg.Tracker.TerminalStates) {
				slog.Info("issue moved to terminal state, stopping agent", "issue", issueID, "state", issues[0].State)
				o.State.RemoveRunning(issueID)
				o.State.ReleaseClaim(issueID)
				o.addRuntime(info)
				// Clean up workspace (local or remote).
				o.removeWorkspace(ctx, cfg, info)
			}
		}
	}
}

// removeWorkspace removes the workspace for a running issue (local or remote).
func (o *Orchestrator) removeWorkspace(ctx context.Context, cfg config.Schema, info *RunInfo) {
	if info.WorkspacePath == "" {
		return
	}
	hookTimeout := hookTimeoutVal(cfg)
	if workspace.IsRemote(info.WorkerHost) {
		sshCfg := sshConfigFromHost(info.WorkerHost)
		_ = workspace.RemoveRemote(ctx, sshCfg, info.WorkspacePath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout)
	} else {
		_ = workspace.Remove(ctx, info.WorkspacePath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout)
	}
}

// addRuntime accumulates ended-session runtime into the state counter.
func (o *Orchestrator) addRuntime(info *RunInfo) {
	runtimeMs := time.Since(info.StartedAt).Milliseconds()
	o.State.AddRuntimeMs(runtimeMs)
}

// processRetries dispatches issues whose retry timer has fired.
func (o *Orchestrator) processRetries(ctx context.Context, cfg config.Schema) {
	pending := o.State.PendingRetries()
	now := time.Now()

	for issueID, entry := range pending {
		if now.Before(entry.FireAt) {
			continue // not yet due
		}

		o.State.RemoveRetry(issueID)

		// Re-check if issue is still active.
		if o.Tracker != nil {
			issues, err := o.Tracker.FetchCandidateIssues(ctx)
			if err != nil {
				slog.Warn("retry: failed to fetch candidates", "error", err)
				continue
			}
			found := false
			for _, iss := range issues {
				if iss.ID == issueID {
					found = true
					entry.Issue = iss // refresh issue data
					break
				}
			}
			if !found {
				slog.Info("retry: issue no longer in candidate list, releasing claim", "issue", issueID)
				o.State.ReleaseClaim(issueID)
				continue
			}
		}

		// Check concurrency limits before re-dispatching.
		if !o.canDispatch(cfg, entry.Issue) {
			slog.Info("retry: no available slots, rescheduling", "issue", issueID)
			entry.Attempt++
			entry.FireAt = time.Now().Add(failureRetryDelay(entry.Attempt, cfg))
			o.State.SetRetry(issueID, entry)
			continue
		}

		o.doDispatch(ctx, cfg, entry.Issue, entry.Attempt)
	}
}

// dispatch sorts candidates and dispatches eligible issues.
func (o *Orchestrator) dispatch(ctx context.Context, cfg config.Schema, candidates []tracker.Issue) {
	sortIssues(candidates)

	for _, issue := range candidates {
		if !o.canDispatch(cfg, issue) {
			continue
		}
		o.doDispatch(ctx, cfg, issue, 1)
	}
}

// canDispatch checks whether an issue can be dispatched given concurrency
// limits, claim status, running status, and blocker rules.
func (o *Orchestrator) canDispatch(cfg config.Schema, issue tracker.Issue) bool {
	// Already running or claimed?
	if o.State.IsRunning(issue.ID) || o.State.IsClaimed(issue.ID) {
		return false
	}

	// Already completed?
	if o.State.IsCompleted(issue.ID) {
		return false
	}

	// Not in an active state?
	if !isActiveState(issue.State, cfg.Tracker.ActiveStates) {
		return false
	}

	// Blocker rule: Todo issues with non-terminal blockers are not eligible.
	if strings.EqualFold(issue.State, "todo") && o.isBlockedByNonTerminal(issue, cfg.Tracker.TerminalStates) {
		return false
	}

	// Global concurrency limit.
	maxConcurrent := cfg.Agent.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	if o.State.RunningCount() >= maxConcurrent {
		return false
	}

	// Per-state concurrency limit (keys normalized to lowercase).
	stateCounts := o.State.RunningByState()
	if limit, ok := cfg.Agent.MaxConcurrentByState[strings.ToLower(issue.State)]; ok && limit > 0 {
		if stateCounts[strings.ToLower(issue.State)] >= limit {
			return false
		}
	}

	return true
}

// isBlockedByNonTerminal returns true if the issue has any blocker that is not
// in a terminal state. Unknown blocker states are treated as non-terminal (safe default).
func (o *Orchestrator) isBlockedByNonTerminal(issue tracker.Issue, terminalStates []string) bool {
	for _, blocker := range issue.BlockedBy {
		if blocker.State == "" {
			// Unknown state — treat as non-terminal to be safe.
			return true
		}
		if !isTerminalState(blocker.State, terminalStates) {
			return true
		}
	}
	return false
}

// doDispatch starts an agent run for the given issue in a goroutine.
func (o *Orchestrator) doDispatch(ctx context.Context, cfg config.Schema, issue tracker.Issue, attempt int) {
	o.State.Claim(issue.ID)

	workerHost := o.selectWorkerHost(cfg)

	runner := &agentrunner.Runner{
		Agent:      o.Agent,
		Tracker:    o.Tracker,
		Cfg:        cfg,
		Tmpl:       o.Tmpl(),
		WorkerHost: workerHost,
	}

	// Create workspace (local or remote).
	hookTimeout := hookTimeoutVal(cfg)
	var wsPath string
	var createdNow bool
	var err error
	sshCfg := sshConfigFromHost(workerHost)
	if workspace.IsRemote(workerHost) {
		wsPath, createdNow, err = workspace.CreateRemote(ctx, sshCfg, cfg.Workspace.Root, issue.Identifier, cfg.Hooks.AfterCreate, hookTimeout)
	} else {
		wsPath, createdNow, err = workspace.Create(ctx, cfg.Workspace.Root, issue.Identifier, cfg.Hooks.AfterCreate, hookTimeout)
	}
	if err != nil {
		slog.Error("create workspace for dispatch", "issue", issue.ID, "error", err, "host", workerHost)
		// Clean up if partially created remotely.
		if workspace.IsRemote(workerHost) && wsPath != "" {
			_ = workspace.RemoveRemote(ctx, sshCfg, wsPath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout)
		}
		o.State.ReleaseClaim(issue.ID)
		return
	}

	_ = createdNow // tracked for future hook optimization

	info := &RunInfo{
		Issue:         issue,
		WorkerHost:    workerHost,
		WorkspacePath: wsPath,
		Attempt:       attempt,
		StartedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
	o.State.SetRunning(issue.ID, info)

	eventCh := make(chan agent.Event, 64)

	// Start the runner in a goroutine.
	go func() {
		result, err := runner.Run(ctx, issue, attempt, eventCh)
		close(eventCh)

		// On completion/failure, update state.
		runInfo, wasRunning := o.State.RemoveRunning(issue.ID)
		if !wasRunning {
			return // already cleaned up (stall/reconcile)
		}

		if err != nil {
			slog.Error("agent run failed", "issue", issue.ID, "error", err, "attempt", attempt)
			o.scheduleRetry(issue.ID, runInfo, false, cfg)
		o.addRuntime(runInfo)
			o.State.UpdateLastError(issue.ID, err.Error())
			return
		}

		if result.Completed {
			o.State.MarkCompleted(issue.ID)
			o.addRuntime(runInfo)
			slog.Info("agent run completed", "issue", issue.ID, "turns", result.TotalTurns)
			// Check if issue is still active for continuation retry.
			if o.Tracker != nil && isActiveState(issue.State, cfg.Tracker.ActiveStates) {
				o.scheduleRetry(issue.ID, runInfo, true, cfg)
			} else {
				o.State.ReleaseClaim(issue.ID)
			}
			return
		}

		// Not completed but no error — hit max turns or similar.
		o.addRuntime(runInfo)
		o.scheduleRetry(issue.ID, runInfo, true, cfg)
	}()

	// Drain events in a separate goroutine to update state.
	go func() {
		for evt := range eventCh {
			o.State.UpdateActivity(issue.ID)
			if evt.Usage != nil {
				o.State.UpdateUsage(issue.ID, *evt.Usage)
			}
		if evt.SessionID != "" {
					o.State.UpdateSessionID(issue.ID, evt.SessionID)
			}
			if evt.RateLimits != nil {
				o.State.SetRateLimits(evt.RateLimits)
			}
			if evt.Type == agent.EventTurnFailed {
				o.State.UpdateLastError(issue.ID, evt.Message)
			}
		}
	}()
}

// scheduleRetry schedules a retry for an issue.
func (o *Orchestrator) scheduleRetry(issueID string, info *RunInfo, isContinue bool, cfg config.Schema) {
	attempt := info.Attempt
	if !isContinue {
		attempt++
	}

	var delay time.Duration
	if isContinue {
		delay = continuationRetryDelay
	} else {
		delay = failureRetryDelay(attempt, cfg)
	}

	o.State.SetRetry(issueID, &RetryEntry{
		Issue:      info.Issue,
		Attempt:    attempt,
		FireAt:     time.Now().Add(delay),
		IsContinue: isContinue,
	})
	slog.Info("scheduled retry", "issue", issueID, "attempt", attempt, "delay", delay, "continuation", isContinue)
}

// failureRetryDelay computes exponential backoff for failure retries.
func failureRetryDelay(attempt int, cfg config.Schema) time.Duration {
	power := min(attempt-1, 10)
	delay := failureRetryBase * time.Duration(math.Pow(2, float64(power)))

	maxBackoff := maxBackoff(cfg)
	if maxBackoff > 0 && delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

// startupCleanup removes workspaces for issues that are in terminal states.
func (o *Orchestrator) startupCleanup(ctx context.Context, cfg config.Schema) {
	if o.Tracker == nil {
		return
	}
	terminalStates := cfg.Tracker.TerminalStates
	if len(terminalStates) == 0 {
		return
	}

	issues, err := o.Tracker.FetchIssuesByStates(ctx, terminalStates)
	if err != nil {
		slog.Warn("startup cleanup: failed to fetch terminal issues", "error", err)
		return
	}

	for _, iss := range issues {
		if iss.Identifier == "" {
			continue
		}
		key := trackerSanitizeKey(iss.Identifier)
		wsPath := cfg.Workspace.Root + "/" + key
		hookTimeout := hookTimeoutVal(cfg)
		if err := workspace.Remove(ctx, wsPath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout); err != nil {
			slog.Warn("startup cleanup: failed to remove workspace", "issue", iss.Identifier, "error", err)
		}
	}
}

// sortIssues sorts candidates by priority (nil = last), then creation date,
// then identifier.
func sortIssues(issues []tracker.Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		// Priority: nil comes last.
		pi, pj := issues[i].Priority, issues[j].Priority
		if pi != nil && pj != nil {
			if *pi != *pj {
				return *pi < *pj
			}
		} else if pi != nil {
			return true // i has priority, j doesn't
		} else if pj != nil {
			return false // j has priority, i doesn't
		}

		// Creation date: older first.
		if !issues[i].CreatedAt.IsZero() && !issues[j].CreatedAt.IsZero() && issues[i].CreatedAt != issues[j].CreatedAt {
			return issues[i].CreatedAt.Before(issues[j].CreatedAt)
		}

		// Identifier: lexicographic.
		return issues[i].Identifier < issues[j].Identifier
	})
}

// isActiveState checks if a state is in the active states list.
// If no active states are configured, all states are considered active.
func isActiveState(state string, activeStates []string) bool {
	if len(activeStates) == 0 {
		return true
	}
	return slices.Contains(activeStates, state)
}

// isTerminalState checks if a state is in the terminal states list.
func isTerminalState(state string, terminalStates []string) bool {
	return slices.Contains(terminalStates, state)
}

// cfgPollInterval returns the configured poll interval with a sensible default.
func cfgPollInterval(cfg config.Schema) time.Duration {
	if cfg.Polling.IntervalMS > 0 {
		return time.Duration(cfg.Polling.IntervalMS) * time.Millisecond
	}
	return 30 * time.Second
}

// stallTimeout returns the configured stall timeout. 0 or negative disables.
func stallTimeout(cfg config.Schema) time.Duration {
	ms := cfg.Agent.Codex.StallTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// maxBackoff returns the configured max retry backoff.
func maxBackoff(cfg config.Schema) time.Duration {
	if cfg.Agent.MaxRetryBackoffMS > 0 {
		return time.Duration(cfg.Agent.MaxRetryBackoffMS) * time.Millisecond
	}
	return 5 * time.Minute
}

// hookTimeoutVal returns the hook timeout from config.
func hookTimeoutVal(cfg config.Schema) time.Duration {
	if cfg.Hooks.TimeoutMS > 0 {
		return time.Duration(cfg.Hooks.TimeoutMS) * time.Millisecond
	}
	return 5 * time.Minute
}

// trackerSanitizeKey is a simple key sanitizer for workspace paths.
func trackerSanitizeKey(s string) string {
	var b []byte
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b = append(b, byte(r))
		} else if r == ' ' || r == '/' {
			b = append(b, '-')
		}
	}
	return string(b)
}

// sshConfigFromHost parses a "host:port" or "host" string into an sshclient.Config.
func sshConfigFromHost(host string) sshclient.Config {
	cfg := sshclient.Config{Host: host}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		cfg.Host = host[:idx]
		var port int
		if _, err := fmt.Sscanf(host[idx+1:], "%d", &port); err == nil && port > 0 {
			cfg.Port = port
		}
	}
	return cfg
}
