package orchestrator

import (
	"strings"
	"sync"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/tracker"
)

// RunInfo tracks an in-flight agent run for an issue.
type RunInfo struct {
	Issue         tracker.Issue
	WorkerHost    string
	WorkspacePath string
	Attempt       int
	StartedAt     time.Time
	LastActivity  time.Time // last Codex/agent activity timestamp
	TurnCount     int
	TotalUsage    agent.UsageReport
	SessionID     string // "<thread_id>-<turn_id>" for observability
	LastError     string // most recent error message for this run
}

// RetryEntry tracks a pending retry for an issue.
type RetryEntry struct {
	Issue      tracker.Issue
	Attempt    int
	FireAt     time.Time
	IsContinue bool // true = continuation retry (1s delay), false = failure backoff
}

// State holds the orchestrator's runtime state.
type State struct {
	mu             sync.RWMutex
	running        map[string]*RunInfo    // issueID -> RunInfo
	claimed        map[string]struct{}     // issueID set: issues claimed for dispatch/retry
	retryAttempts  map[string]*RetryEntry  // issueID -> pending retry
	completed      map[string]struct{}     // issueID set: successfully completed
	totalRuntimeMs int64                   // cumulative ended-session runtime in milliseconds
	rateLimits     map[string]any          // latest rate-limit payload from agent
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
	defer s.mu.Unlock()
	s.running[issueID] = info
}

// RemoveRunning removes a running entry and returns it.
func (s *State) RemoveRunning(issueID string) (*RunInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.running[issueID]
	if ok {
		delete(s.running, issueID)
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

// Claim marks an issue as claimed for dispatch/retry.
func (s *State) Claim(issueID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed[issueID] = struct{}{}
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
	defer s.mu.Unlock()
	delete(s.claimed, issueID)
}

// SetRetry sets a pending retry for an issue.
func (s *State) SetRetry(issueID string, entry *RetryEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retryAttempts[issueID] = entry
}

// RemoveRetry removes a pending retry and returns it.
func (s *State) RemoveRetry(issueID string) (*RetryEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.retryAttempts[issueID]
	if ok {
		delete(s.retryAttempts, issueID)
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
	defer s.mu.Unlock()
	s.completed[issueID] = struct{}{}
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
	defer s.mu.Unlock()
	if info, ok := s.running[issueID]; ok {
		info.LastActivity = time.Now()
	}
}

// UpdateUsage updates the usage counters for a running issue.
func (s *State) UpdateUsage(issueID string, usage agent.UsageReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.running[issueID]; ok {
		info.TotalUsage.InputTokens += usage.InputTokens
		info.TotalUsage.OutputTokens += usage.OutputTokens
		info.TotalUsage.TotalTokens += usage.TotalTokens
		info.TurnCount++
	}
}

// UpdateSessionID updates the session ID for a running issue.
func (s *State) UpdateSessionID(issueID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.running[issueID]; ok {
		info.SessionID = sessionID
	}
}

// UpdateLastError updates the last error message for a running issue.
func (s *State) UpdateLastError(issueID, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.running[issueID]; ok {
		info.LastError = errMsg
	}
}

// AddRuntimeMs adds ended-session runtime to the cumulative total.
func (s *State) AddRuntimeMs(ms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalRuntimeMs += ms
}

// TotalRuntimeSeconds returns the aggregate runtime in seconds (ended sessions + active).
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
	defer s.mu.Unlock()
	s.rateLimits = limits
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
