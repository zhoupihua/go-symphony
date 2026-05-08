package orchestrator

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zhoupihua/go-symphony/internal/agent"
	"github.com/zhoupihua/go-symphony/internal/ha"
	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// RunInfo tracks an in-flight agent run for an issue.
type RunInfo struct {
	Issue         tracker.Issue
	WorkerHost    string
	WorkspacePath string
	Attempt       int
	StartedAt     time.Time
	LastActivity  time.Time
	TurnCount     int
	TotalUsage    agent.UsageReport
	SessionID     string
	LastError     string
	Phase         string

	LastReportedInputTokens  int64
	LastReportedOutputTokens int64
	LastReportedTotalTokens  int64
}

// RetryEntry tracks a pending retry for an issue.
type RetryEntry struct {
	Issue      tracker.Issue
	Attempt    int
	FireAt     time.Time
	IsContinue bool
}

// runInfoDTO is the serializable form of RunInfo for state replication.
type runInfoDTO struct {
	IssueID      string   `json:"issue_id"`
	Identifier   string   `json:"identifier"`
	Title        string   `json:"title"`
	State        string   `json:"state"`
	Labels       []string `json:"labels"`
	URL          string   `json:"url"`
	BranchName   string   `json:"branch_name"`
	WorkerHost   string   `json:"worker_host"`
	WorkspacePath string  `json:"workspace_path"`
	Attempt      int      `json:"attempt"`
	StartedAt    string   `json:"started_at"`
	LastActivity string   `json:"last_activity"`
	TurnCount    int      `json:"turn_count"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	TotalTokens  int64    `json:"total_tokens"`
	SessionID    string   `json:"session_id"`
	LastError    string   `json:"last_error"`
	Phase        string   `json:"phase"`
	LastRepInputTokens  int64 `json:"last_rep_input_tokens"`
	LastRepOutputTokens int64 `json:"last_rep_output_tokens"`
	LastRepTotalTokens  int64 `json:"last_rep_total_tokens"`
}

// retryEntryDTO is the serializable form of RetryEntry for state replication.
type retryEntryDTO struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	State      string `json:"state"`
	Attempt    int    `json:"attempt"`
	FireAt     string `json:"fire_at"`
	IsContinue bool   `json:"is_continue"`
}

func runInfoToDTO(info *RunInfo) runInfoDTO {
	return runInfoDTO{
		IssueID:      info.Issue.ID,
		Identifier:   info.Issue.Identifier,
		Title:        info.Issue.Title,
		State:        info.Issue.State,
		Labels:       info.Issue.Labels,
		URL:          info.Issue.URL,
		BranchName:   info.Issue.BranchName,
		WorkerHost:   info.WorkerHost,
		WorkspacePath: info.WorkspacePath,
		Attempt:      info.Attempt,
		StartedAt:    info.StartedAt.UTC().Format(time.RFC3339Nano),
		LastActivity: info.LastActivity.UTC().Format(time.RFC3339Nano),
		TurnCount:    info.TurnCount,
		InputTokens:  info.TotalUsage.InputTokens,
		OutputTokens: info.TotalUsage.OutputTokens,
		TotalTokens:  info.TotalUsage.TotalTokens,
		SessionID:    info.SessionID,
		LastError:    info.LastError,
		Phase:        info.Phase,
		LastRepInputTokens:  info.LastReportedInputTokens,
		LastRepOutputTokens: info.LastReportedOutputTokens,
		LastRepTotalTokens:  info.LastReportedTotalTokens,
	}
}

func dtoToRunInfo(dto runInfoDTO) *RunInfo {
	startedAt, _ := time.Parse(time.RFC3339Nano, dto.StartedAt)
	lastActivity, _ := time.Parse(time.RFC3339Nano, dto.LastActivity)
	return &RunInfo{
		Issue: tracker.Issue{
			ID:         dto.IssueID,
			Identifier: dto.Identifier,
			Title:      dto.Title,
			State:      dto.State,
			Labels:     dto.Labels,
			URL:        dto.URL,
			BranchName: dto.BranchName,
		},
		WorkerHost:   dto.WorkerHost,
		WorkspacePath: dto.WorkspacePath,
		Attempt:      dto.Attempt,
		StartedAt:    startedAt,
		LastActivity: lastActivity,
		TurnCount:    dto.TurnCount,
		TotalUsage: agent.UsageReport{
			InputTokens:  dto.InputTokens,
			OutputTokens: dto.OutputTokens,
			TotalTokens:  dto.TotalTokens,
		},
		SessionID:    dto.SessionID,
		LastError:    dto.LastError,
		Phase:        dto.Phase,
		LastReportedInputTokens:  dto.LastRepInputTokens,
		LastReportedOutputTokens: dto.LastRepOutputTokens,
		LastReportedTotalTokens:  dto.LastRepTotalTokens,
	}
}

func retryEntryToDTO(entry *RetryEntry) retryEntryDTO {
	return retryEntryDTO{
		IssueID:    entry.Issue.ID,
		Identifier: entry.Issue.Identifier,
		State:      entry.Issue.State,
		Attempt:    entry.Attempt,
		FireAt:     entry.FireAt.UTC().Format(time.RFC3339Nano),
		IsContinue: entry.IsContinue,
	}
}

func dtoToRetryEntry(dto retryEntryDTO) *RetryEntry {
	fireAt, _ := time.Parse(time.RFC3339Nano, dto.FireAt)
	return &RetryEntry{
		Issue: tracker.Issue{
			ID:         dto.IssueID,
			Identifier: dto.Identifier,
			State:      dto.State,
		},
		Attempt:    dto.Attempt,
		FireAt:     fireAt,
		IsContinue: dto.IsContinue,
	}
}

// State holds the orchestrator's runtime state.
type State struct {
	mu             sync.RWMutex
	running        map[string]*RunInfo
	claimed        map[string]struct{}
	retryAttempts  map[string]*RetryEntry
	completed      map[string]struct{}
	totalRuntimeMs int64
	rateLimits     map[string]any
	replicator     ha.StateReplicator
}

// NewState creates a new empty State.
func NewState() *State {
	return &State{
		running:       make(map[string]*RunInfo),
		claimed:       make(map[string]struct{}),
		retryAttempts: make(map[string]*RetryEntry),
		completed:     make(map[string]struct{}),
	}
}

// NewStateWithReplicator creates a State with Raft replication enabled.
func NewStateWithReplicator(r ha.StateReplicator) *State {
	s := NewState()
	s.replicator = r
	return s
}

func (s *State) replicate(op, key string, data any) {
	if s.replicator == nil {
		return
	}
	var b []byte
	var err error
	if data != nil {
		b, err = json.Marshal(data)
		if err != nil {
			slog.Warn("replicate marshal", "op", op, "key", key, "error", err)
			return
		}
	}
	if err := s.replicator.ApplyCommand(op, key, b); err != nil {
		slog.Warn("replicate", "op", op, "key", key, "error", err)
	}
}

func (s *State) replicateRunning(issueID string, info *RunInfo) {
	s.replicate(ha.OpSetRunning, issueID, runInfoToDTO(info))
}

// Running returns a snapshot of currently running issues.
func (s *State) Running() map[string]*RunInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*RunInfo, len(s.running))
	for k, v := range s.running {
		out[k] = v
	}
	return out
}

// SetRunning adds or updates a running entry.
func (s *State) SetRunning(issueID string, info *RunInfo) {
	s.mu.Lock()
	s.running[issueID] = info
	s.mu.Unlock()
	s.replicateRunning(issueID, info)
}

// RemoveRunning removes a running entry and returns it.
func (s *State) RemoveRunning(issueID string) (*RunInfo, bool) {
	s.mu.Lock()
	info, ok := s.running[issueID]
	if ok {
		delete(s.running, issueID)
	}
	s.mu.Unlock()
	if ok {
		s.replicate(ha.OpRemoveRunning, issueID, nil)
	}
	return info, ok
}

// IsRunning returns whether an issue is currently being processed.
func (s *State) IsRunning(issueID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.running[issueID]
	return ok
}

// RunningCount returns the number of currently running issues.
func (s *State) RunningCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.running)
}

// RunningByState returns the count of running issues in each state (keys lowercased).
func (s *State) RunningByState() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := make(map[string]int)
	for _, info := range s.running {
		counts[strings.ToLower(info.Issue.State)]++
	}
	return counts
}

// RunningByHost returns the count of running issues on each worker host.
func (s *State) RunningByHost() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := make(map[string]int)
	for _, info := range s.running {
		counts[info.WorkerHost]++
	}
	return counts
}

// Claim marks an issue as claimed for dispatch/retry.
func (s *State) Claim(issueID string) {
	s.mu.Lock()
	s.claimed[issueID] = struct{}{}
	s.mu.Unlock()
	s.replicate(ha.OpClaim, issueID, nil)
}

// IsClaimed returns whether an issue is claimed.
func (s *State) IsClaimed(issueID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.claimed[issueID]
	return ok
}

// ReleaseClaim removes a claim on an issue.
func (s *State) ReleaseClaim(issueID string) {
	s.mu.Lock()
	delete(s.claimed, issueID)
	s.mu.Unlock()
	s.replicate(ha.OpReleaseClaim, issueID, nil)
}

// SetRetry sets a pending retry for an issue.
func (s *State) SetRetry(issueID string, entry *RetryEntry) {
	s.mu.Lock()
	s.retryAttempts[issueID] = entry
	s.mu.Unlock()
	s.replicate(ha.OpAddRetry, issueID, retryEntryToDTO(entry))
}

// RemoveRetry removes a pending retry and returns it.
func (s *State) RemoveRetry(issueID string) (*RetryEntry, bool) {
	s.mu.Lock()
	entry, ok := s.retryAttempts[issueID]
	if ok {
		delete(s.retryAttempts, issueID)
	}
	s.mu.Unlock()
	if ok {
		s.replicate(ha.OpRemoveRetry, issueID, nil)
	}
	return entry, ok
}

// PendingRetries returns all pending retry entries.
func (s *State) PendingRetries() map[string]*RetryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*RetryEntry, len(s.retryAttempts))
	for k, v := range s.retryAttempts {
		out[k] = v
	}
	return out
}

// RetryCount returns the number of pending retries.
func (s *State) RetryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.retryAttempts)
}

// RunningIssue returns the RunInfo for a specific issue, or nil if not running.
func (s *State) RunningIssue(issueID string) *RunInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running[issueID]
}

// MarkCompleted marks an issue as successfully completed.
func (s *State) MarkCompleted(issueID string) {
	s.mu.Lock()
	s.completed[issueID] = struct{}{}
	s.mu.Unlock()
	s.replicate(ha.OpMarkCompleted, issueID, nil)
}

// IsCompleted returns whether an issue has been completed.
func (s *State) IsCompleted(issueID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.completed[issueID]
	return ok
}

// UpdateActivity updates the last activity timestamp for a running issue.
func (s *State) UpdateActivity(issueID string) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		info.LastActivity = time.Now()
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// UpdateUsage updates the usage counters for a running issue using delta tracking.
func (s *State) UpdateUsage(issueID string, usage agent.UsageReport) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		inputDelta := usage.InputTokens - info.LastReportedInputTokens
		outputDelta := usage.OutputTokens - info.LastReportedOutputTokens
		totalDelta := usage.TotalTokens - info.LastReportedTotalTokens

		if inputDelta > 0 {
			info.TotalUsage.InputTokens += inputDelta
		}
		if outputDelta > 0 {
			info.TotalUsage.OutputTokens += outputDelta
		}
		if totalDelta > 0 {
			info.TotalUsage.TotalTokens += totalDelta
		}

		info.LastReportedInputTokens = usage.InputTokens
		info.LastReportedOutputTokens = usage.OutputTokens
		info.LastReportedTotalTokens = usage.TotalTokens
		info.TurnCount++
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// UpdateIssue replaces the Issue snapshot for a running entry.
func (s *State) UpdateIssue(issueID string, issue tracker.Issue) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		info.Issue = issue
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// SetPhase updates the phase for a running issue.
func (s *State) SetPhase(issueID, phase string) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		info.Phase = phase
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// UpdateSessionID updates the session ID for a running issue.
func (s *State) UpdateSessionID(issueID, sessionID string) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		info.SessionID = sessionID
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// UpdateLastError updates the last error message for a running issue.
func (s *State) UpdateLastError(issueID, errMsg string) {
	s.mu.Lock()
	if info, ok := s.running[issueID]; ok {
		info.LastError = errMsg
		s.mu.Unlock()
		s.replicateRunning(issueID, info)
	} else {
		s.mu.Unlock()
	}
}

// AddRuntimeMs adds ended-session runtime to the cumulative total.
func (s *State) AddRuntimeMs(ms int64) {
	s.mu.Lock()
	s.totalRuntimeMs += ms
	s.mu.Unlock()
	// Runtime updates are infrequent; replicate via full state snapshot.
	s.replicateRuntime()
}

func (s *State) replicateRuntime() {
	if s.replicator == nil {
		return
	}
	s.mu.RLock()
	snapshot := struct {
		TotalRuntimeMS int64 `json:"total_runtime_ms"`
	}{TotalRuntimeMS: s.totalRuntimeMs}
	s.mu.RUnlock()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	// Use snapshot_state op with just the runtime field.
	// The FSM's snapshot_state replaces the entire state, so we need
	// a different approach: use a dedicated op or just include it
	// in set_running updates. For now, we skip replicating totalRuntimeMs
	// since it's an observability metric that re-populates on re-discovery.
	_ = data
}

// TotalRuntimeSeconds returns the aggregate runtime in seconds.
func (s *State) TotalRuntimeSeconds() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := float64(s.totalRuntimeMs) / 1000.0
	now := time.Now()
	for _, info := range s.running {
		total += now.Sub(info.StartedAt).Seconds()
	}
	return total
}

// SetRateLimits updates the latest rate-limit payload.
func (s *State) SetRateLimits(limits map[string]any) {
	s.mu.Lock()
	s.rateLimits = limits
	s.mu.Unlock()
	// Rate limits are transient; skip replication.
}

// RateLimits returns the latest rate-limit payload.
func (s *State) RateLimits() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rateLimits == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(s.rateLimits))
	for k, v := range s.rateLimits {
		out[k] = v
	}
	return out
}

// RestoreState restores the State from replicated FSM data.
func (s *State) RestoreState(data []byte) error {
	var state struct {
		Running        map[string]json.RawMessage `json:"running"`
		Claimed        map[string]bool            `json:"claimed"`
		Retries        map[string]json.RawMessage `json:"retries"`
		Completed      map[string]bool            `json:"completed"`
		TotalRuntimeMS int64                      `json:"total_runtime_ms"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshal replicated state: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.running = make(map[string]*RunInfo, len(state.Running))
	for k, raw := range state.Running {
		var dto runInfoDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			slog.Warn("restore running entry", "key", k, "error", err)
			continue
		}
		s.running[k] = dtoToRunInfo(dto)
	}

	s.claimed = make(map[string]struct{}, len(state.Claimed))
	for k := range state.Claimed {
		s.claimed[k] = struct{}{}
	}

	s.retryAttempts = make(map[string]*RetryEntry, len(state.Retries))
	for k, raw := range state.Retries {
		var dto retryEntryDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			slog.Warn("restore retry entry", "key", k, "error", err)
			continue
		}
		s.retryAttempts[k] = dtoToRetryEntry(dto)
	}

	s.completed = make(map[string]struct{}, len(state.Completed))
	for k := range state.Completed {
		s.completed[k] = struct{}{}
	}

	s.totalRuntimeMs = state.TotalRuntimeMS

	return nil
}
