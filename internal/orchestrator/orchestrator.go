package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/zhoupihua/go-symphony/internal/agent"
	"github.com/zhoupihua/go-symphony/internal/agentrunner"
	"github.com/zhoupihua/go-symphony/internal/config"
	"github.com/zhoupihua/go-symphony/internal/ha"
	"github.com/zhoupihua/go-symphony/internal/sshclient"
	"github.com/zhoupihua/go-symphony/internal/tracker"
	"github.com/zhoupihua/go-symphony/internal/workspace"
)

const (
	continuationRetryDelay = 1 * time.Second
	failureRetryBase       = 10 * time.Second
)

// Phase constants track the lifecycle of an agent run.
const (
	PhaseInitializing        = "initializing"
	PhaseCreatingWorkspace   = "creating_workspace"
	PhaseStartingSession     = "starting_session"
	PhaseRunningTurns        = "running_turns"
	PhaseSucceeded           = "succeeded"
	PhaseFailed              = "failed"
	PhaseTimedOut            = "timed_out"
	PhaseStalled             = "stalled"
	PhaseCanceledByReconcile = "canceled_by_reconciliation"
)

// Orchestrator is the main coordination loop. It polls the tracker for
// candidate issues, dispatches them to agent runners up to concurrency limits,
// and reconciles running issues for stall detection and state changes.
type Orchestrator struct {
	Tracker     tracker.Tracker
	Agent       agent.Agent
	Elector     ha.Elector
	Cfg         func() config.Schema // called each tick for hot-reload support
	Tmpl        func() string        // called each tick for hot-reload support
	State       *State
	nextHostIdx int // round-robin index for SSH host selection
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

// SetReplicator enables state replication for HA deployments.
// Must be called before Run(). If the elector implements StateReplicator,
// it is used to replicate all state mutations and restore state on failover.
func (o *Orchestrator) SetReplicator(r ha.StateReplicator) {
	o.State.replicator = r
}

// RestoreFromReplicator restores orchestrator state from the replicated FSM.
// Called after winning an election to recover in-flight state.
func (o *Orchestrator) RestoreFromReplicator() {
	if o.State.replicator == nil {
		return
	}
	data, err := o.State.replicator.ReplicatedState()
	if err != nil {
		slog.Warn("restore from replicator: failed to get state", "error", err)
		return
	}
	if len(data) == 0 {
		return
	}
	if err := o.State.RestoreState(data); err != nil {
		slog.Warn("restore from replicator: failed to restore", "error", err)
	} else {
		slog.Info("restored orchestrator state from replicator", "running", o.State.RunningCount(), "retries", o.State.RetryCount())
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
		// Watch for leadership loss in HA mode.
		var doneCh <-chan struct{}
		if o.Elector != nil {
			doneCh = o.Elector.Done()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-doneCh:
			// Leadership lost — re-campaign and restore state.
			slog.Warn("leadership lost, re-campaigning")
			if err := o.Elector.Campaign(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				slog.Error("re-campaign failed", "error", err)
				return err
			}
			o.RestoreFromReplicator()
			continue
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

	// 3. Validate dispatch config before fetching candidates.
	if err := validateDispatchConfig(cfg); err != nil {
		slog.Warn("dispatch config invalid, skipping fetch", "error", err)
		return
	}

	// 4. Fetch candidate issues.
	candidates, err := o.Tracker.FetchCandidateIssues(ctx)
	if err != nil {
		slog.Error("fetch candidate issues", "error", err)
		return
	}

	// 5. Sort and dispatch eligible.
	o.dispatch(ctx, cfg, candidates)
}

// validateDispatchConfig checks that the config has the minimum required fields
// for dispatching work. This prevents wasted API calls when the config is
// incomplete (e.g., during hot-reload of a partially-edited WORKFLOW.md).
func validateDispatchConfig(cfg config.Schema) error {
	if cfg.Tracker.Kind == "" {
		return fmt.Errorf("tracker.kind is empty")
	}
	if cfg.Tracker.APIKey == "" {
		return fmt.Errorf("tracker.api_key is empty")
	}
	if cfg.Tracker.Kind == "linear" && cfg.Tracker.Linear.ProjectSlug == "" {
		return fmt.Errorf("tracker.linear.project_slug is empty")
	}
	if cfg.Agent.Kind == "" {
		return fmt.Errorf("agent.kind is empty")
	}
	if cfg.Agent.Kind == "codex" && cfg.Agent.Codex.Command == "" {
		return fmt.Errorf("agent.codex.command is empty")
	}
	if cfg.Agent.Kind == "claude" && cfg.Agent.Claude.Command == "" {
		return fmt.Errorf("agent.claude.command is empty")
	}
	return nil
}

// reconcileRunning checks all running issues for stalls and state changes.
// It batch-fetches all running issue IDs from the tracker and applies a
// 3-way branch: terminal -> terminate + cleanup, active -> update snapshot,
// neither -> terminate without cleanup.
func (o *Orchestrator) reconcileRunning(ctx context.Context, cfg config.Schema) {
	running := o.State.Running()
	if len(running) == 0 {
		return
	}

	stallTimeout := stallTimeout(cfg)

	// Phase 1: Stall detection — collect IDs of non-stalled issues.
	var nonStalledIDs []string
	for issueID, info := range running {
		if stallTimeout > 0 {
			elapsed := time.Since(info.LastActivity)
			if elapsed > stallTimeout {
				slog.Warn("stall detected, terminating agent", "issue_id", issueID, "issue_identifier", info.Issue.Identifier, "elapsed", elapsed)
				o.State.SetPhase(issueID, PhaseStalled)
				o.State.RemoveRunning(issueID)
				o.State.ReleaseClaim(issueID)
				o.addRuntime(info)
				o.scheduleRetry(issueID, info, false, cfg, "stall detected")
				continue
			}
		}
		nonStalledIDs = append(nonStalledIDs, issueID)
	}

	// Phase 2: Batch-fetch all non-stalled issue states.
	if o.Tracker == nil || len(nonStalledIDs) == 0 {
		return
	}

	fetched, err := o.Tracker.FetchIssueStatesByIDs(ctx, nonStalledIDs)
	if err != nil {
		slog.Warn("reconcile: batch fetch failed", "error", err)
		return
	}

	// Build a lookup map from fetched issues.
	fetchedMap := make(map[string]tracker.Issue, len(fetched))
	for _, iss := range fetched {
		fetchedMap[iss.ID] = iss
	}

	// Phase 3: 3-way branch for each non-stalled running issue.
	for _, issueID := range nonStalledIDs {
		info, ok := running[issueID]
		if !ok {
			continue // removed during stall phase
		}

		refreshed, found := fetchedMap[issueID]
		if !found {
			slog.Warn("reconcile: issue not found in tracker response", "issue_id", issueID, "issue_identifier", info.Issue.Identifier)
			continue
		}

		if isTerminalState(refreshed.State, cfg.Tracker.TerminalStates) {
			// Terminal: terminate worker + clean up workspace.
			slog.Info("issue moved to terminal state, stopping agent", "issue_id", issueID, "issue_identifier", info.Issue.Identifier, "state", refreshed.State)
			o.State.SetPhase(issueID, PhaseCanceledByReconcile)
			o.State.RemoveRunning(issueID)
			o.State.ReleaseClaim(issueID)
			o.addRuntime(info)
			o.removeWorkspace(ctx, cfg, info)
		} else if isActiveState(refreshed.State, cfg.Tracker.ActiveStates) {
			// Active: update issue snapshot in RunInfo.
			o.State.UpdateIssue(issueID, refreshed)
		} else {
			// Neither active nor terminal (e.g., "In Review", "Triage"):
			// terminate worker but don't clean up workspace — the issue
			// might return to an active state later.
			slog.Info("issue in non-active non-terminal state, stopping agent without cleanup", "issue_id", issueID, "issue_identifier", info.Issue.Identifier, "state", refreshed.State)
			o.State.SetPhase(issueID, PhaseCanceledByReconcile)
			o.State.RemoveRunning(issueID)
			o.State.ReleaseClaim(issueID)
			o.addRuntime(info)
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

// addRuntime accumulates ended-session runtime and token totals into the state counters.
func (o *Orchestrator) addRuntime(info *RunInfo) {
	runtimeMs := time.Since(info.StartedAt).Milliseconds()
	o.State.AddRuntimeMs(runtimeMs)
	o.State.AddTokens(info.TotalUsage.InputTokens, info.TotalUsage.OutputTokens, info.TotalUsage.TotalTokens)
}

// processRetries dispatches issues whose retry timer has fired.
// Per SPEC §16.6, the retry handler only checks concurrency slots,
// NOT the claimed set — the claim is held across the retry cycle.
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
				slog.Warn("retry: failed to fetch candidates, rescheduling", "error", err, "issue_id", issueID, "issue_identifier", entry.Issue.Identifier)
				// Reschedule retry with incremented attempt per SPEC §16.6.
				entry.Attempt++
				entry.FireAt = time.Now().Add(failureRetryDelay(entry.Attempt, cfg))
				entry.Error = "retry poll failed"
				o.State.SetRetry(issueID, entry)
				continue
			}
			found := false
			for _, iss := range issues {
				if strings.EqualFold(iss.ID, issueID) {
					found = true
					entry.Issue = iss // refresh issue data
					break
				}
			}
			if !found {
				slog.Info("retry: issue no longer in candidate list, releasing claim", "issue_id", issueID, "issue_identifier", entry.Issue.Identifier)
				o.State.ReleaseClaim(issueID)
				continue
			}
		}

		// Check only concurrency limits (NOT claimed) per SPEC §16.6.
		// The claim stays held across the retry cycle; canDispatch checks
		// claimed which would deadlock continuation retries.
		maxConcurrent := cfg.Agent.MaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = 10
		}
		hasSlots := o.State.RunningCount() < maxConcurrent
		if hasSlots {
			stateCounts := o.State.RunningByState()
			if limit, ok := cfg.Agent.MaxConcurrentByState[strings.ToLower(entry.Issue.State)]; ok && limit > 0 {
				if stateCounts[strings.ToLower(entry.Issue.State)] >= limit {
					hasSlots = false
				}
			}
		}
		if !hasSlots {
			slog.Info("retry: no available slots, rescheduling", "issue_id", issueID, "issue_identifier", entry.Issue.Identifier)
			if entry.IsContinue {
				// Continuation retries keep their short fixed delay.
				entry.FireAt = time.Now().Add(continuationRetryDelay)
			} else {
				entry.Attempt++
				entry.FireAt = time.Now().Add(failureRetryDelay(entry.Attempt, cfg))
			}
			entry.Error = "no available orchestrator slots"
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
		o.doDispatch(ctx, cfg, issue, 0)
	}
}

// canDispatch checks whether an issue can be dispatched given concurrency
// limits, claim status, running status, and blocker rules.
func (o *Orchestrator) canDispatch(cfg config.Schema, issue tracker.Issue) bool {
	// Already running or claimed?
	if o.State.IsRunning(issue.ID) || o.State.IsClaimed(issue.ID) {
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

	// Per-host concurrency limit.
	if cfg.Worker.MaxConcurrentAgentsPerHost > 0 && len(cfg.Worker.SSHHosts) > 0 {
		hostCounts := o.State.RunningByHost()
		allFull := true
		for _, host := range cfg.Worker.SSHHosts {
			if hostCounts[host] < cfg.Worker.MaxConcurrentAgentsPerHost {
				allFull = false
				break
			}
		}
		if allFull {
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
	// Preflight validation: ensure required issue fields are present (SPEC §4.1.1).
	if issue.ID == "" || issue.Identifier == "" || issue.Title == "" || issue.State == "" {
		slog.Error("skipping issue with missing required fields",
			"issue_id", issue.ID,
			"issue_identifier", issue.Identifier,
			"title", issue.Title,
			"state", issue.State,
		)
		return
	}

	o.State.Claim(issue.ID)

	workerHost := o.selectWorkerHost(cfg)

	// Set running with phase before workspace creation so the dashboard
	// shows the issue immediately.
	o.State.SetRunning(issue.ID, &RunInfo{
		Issue:        issue,
		WorkerHost:   workerHost,
		Attempt:      attempt,
		StartedAt:    time.Now(),
		LastActivity: time.Now(),
		Phase:        PhaseCreatingWorkspace,
	})

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
		slog.Error("create workspace for dispatch", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "error", err, "host", workerHost)
		// Clean up if partially created remotely.
		if workspace.IsRemote(workerHost) && wsPath != "" {
			_ = workspace.RemoveRemote(ctx, sshCfg, wsPath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout)
		}
		o.State.SetPhase(issue.ID, PhaseFailed)
		o.State.RemoveRunning(issue.ID)
		o.State.ReleaseClaim(issue.ID)
		return
	}

	_ = createdNow // tracked for future hook optimization

	// Update workspace path after creation.
	if info := o.State.RunningIssue(issue.ID); info != nil {
		info.WorkspacePath = wsPath
	}
	o.State.SetPhase(issue.ID, PhaseStartingSession)

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
			slog.Error("agent run failed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", runInfo.SessionID, "error", err, "attempt", attempt)
			runInfo.Phase = PhaseFailed
			o.scheduleRetry(issue.ID, runInfo, false, cfg, fmt.Sprintf("worker exited: %v", err))
			o.addRuntime(runInfo)
			return
		}

		if result.Completed {
			o.State.MarkCompleted(issue.ID)
			runInfo.Phase = PhaseSucceeded
			o.addRuntime(runInfo)
			slog.Info("agent run completed", "issue_id", issue.ID, "issue_identifier", issue.Identifier, "session_id", runInfo.SessionID, "turns", result.TotalTurns)
			// Check if issue is still active for continuation retry.
			if o.Tracker != nil && isActiveState(issue.State, cfg.Tracker.ActiveStates) {
				o.scheduleRetry(issue.ID, runInfo, true, cfg, "")
			} else {
				o.State.ReleaseClaim(issue.ID)
			}
			return
		}

		// Not completed but no error — hit max turns or similar.
		o.addRuntime(runInfo)
		o.scheduleRetry(issue.ID, runInfo, true, cfg, "")
	}()

	// Drain events in a separate goroutine to update state.
	go func() {
		for evt := range eventCh {
			o.State.UpdateActivity(issue.ID)
			o.State.SetPhase(issue.ID, PhaseRunningTurns)
			o.State.UpdateLiveSession(issue.ID, "", "", string(evt.Type), evt.Message, evt.PID)
			if evt.Usage != nil {
				o.State.UpdateUsage(issue.ID, *evt.Usage)
			}
			if evt.SessionID != "" {
				o.State.UpdateSessionID(issue.ID, evt.SessionID)
			}
			if evt.RateLimits != nil {
				o.State.SetRateLimitsForIssue(issue.ID, evt.RateLimits)
			}
			if evt.Type == agent.EventTurnFailed {
				o.State.UpdateLastError(issue.ID, evt.Message)
			}
		}
	}()
}

// scheduleRetry schedules a retry for an issue.
// Continuation retries reset attempt to 1; failure retries increment attempt.
func (o *Orchestrator) scheduleRetry(issueID string, info *RunInfo, isContinue bool, cfg config.Schema, errMsg string) {
	var attempt int
	if isContinue {
		attempt = 1
	} else {
		attempt = info.Attempt + 1
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
		Error:      errMsg,
	})
	slog.Info("scheduled retry", "issue_id", issueID, "issue_identifier", info.Issue.Identifier, "attempt", attempt, "delay", delay, "continuation", isContinue)
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
// Per SPEC §8.6, this runs on both local and SSH worker hosts.
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

	hookTimeout := hookTimeoutVal(cfg)

	for _, iss := range issues {
		if iss.Identifier == "" {
			continue
		}
		key := workspace.SanitizeKey(iss.Identifier)
		wsPath := cfg.Workspace.Root + "/" + key

		// Clean local workspace.
		if err := workspace.Remove(ctx, wsPath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout); err != nil {
			slog.Warn("startup cleanup: failed to remove local workspace", "issue_id", iss.ID, "issue_identifier", iss.Identifier, "error", err)
		}

		// Clean remote workspaces on SSH worker hosts.
		for _, host := range cfg.Worker.SSHHosts {
			sshCfg := sshConfigFromHost(host)
			if err := workspace.RemoveRemote(ctx, sshCfg, wsPath, cfg.Workspace.Root, cfg.Hooks.BeforeRemove, hookTimeout); err != nil {
				slog.Warn("startup cleanup: failed to remove remote workspace", "issue_id", iss.ID, "issue_identifier", iss.Identifier, "host", host, "error", err)
			}
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
// Comparison is case-insensitive per SPEC §4.2.
// If no active states are configured, all states are considered active.
func isActiveState(state string, activeStates []string) bool {
	if len(activeStates) == 0 {
		return true
	}
	lower := strings.ToLower(state)
	for _, s := range activeStates {
		if strings.ToLower(s) == lower {
			return true
		}
	}
	return false
}

// isTerminalState checks if a state is in the terminal states list.
// Comparison is case-insensitive per SPEC §4.2.
func isTerminalState(state string, terminalStates []string) bool {
	lower := strings.ToLower(state)
	for _, s := range terminalStates {
		if strings.ToLower(s) == lower {
			return true
		}
	}
	return false
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
	return 60 * time.Second
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
