package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// handleHealthz returns a simple health check response.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// stateResponse is the JSON response for the /api/v1/state endpoint.
type stateResponse struct {
	Leader             bool                   `json:"leader"`
	LeaderAddr         string                 `json:"leader_addr,omitempty"`
	RunningCount       int                    `json:"running_count"`
	RetryCount         int                    `json:"retry_count"`
	TotalRuntimeSeconds float64               `json:"total_runtime_seconds"`
	RateLimits         map[string]any         `json:"rate_limits,omitempty"`
	Running            map[string]runInfoJSON `json:"running"`
	RetryQueue         []retryEntryJSON       `json:"retry_queue,omitempty"`
}

// runInfoJSON is the JSON representation of an orchestrator.RunInfo for the API.
type runInfoJSON struct {
	IssueID       string   `json:"issue_id"`
	Identifier    string   `json:"identifier"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	Labels        []string `json:"labels"`
	URL           string   `json:"url"`
	WorkerHost    string   `json:"worker_host"`
	WorkspacePath string   `json:"workspace_path"`
	Attempt       int      `json:"attempt"`
	StartedAt     string   `json:"started_at"`
	LastActivity  string   `json:"last_activity"`
	TurnCount     int      `json:"turn_count"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	TotalTokens   int64    `json:"total_tokens"`
	SessionID     string   `json:"session_id,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
}

// retryEntryJSON is the JSON representation of an orchestrator.RetryEntry.
type retryEntryJSON struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Attempt    int    `json:"attempt"`
	FireAt     string `json:"fire_at"`
	IsContinue bool   `json:"is_continue"`
}

// handleState returns the current orchestrator state as JSON.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := s.buildStateResponse()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("httpserver: encode state response", "error", err)
	}
}

// handleIssue returns the state of a specific issue by identifier.
func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	identifier := r.PathValue("identifier")
	if identifier == "" {
		http.Error(w, "missing identifier", http.StatusBadRequest)
		return
	}

	// Search through running issues for the matching identifier.
	running := s.state.Running()
	for _, info := range running {
		if info.Issue.Identifier == identifier {
			labels := info.Issue.Labels
			if labels == nil {
				labels = []string{}
			}
			resp := runInfoJSON{
				IssueID:       info.Issue.ID,
				Identifier:    info.Issue.Identifier,
				Title:         info.Issue.Title,
				State:         info.Issue.State,
				Labels:        labels,
				URL:           info.Issue.URL,
				WorkerHost:    info.WorkerHost,
				WorkspacePath: info.WorkspacePath,
				Attempt:       info.Attempt,
				StartedAt:     info.StartedAt.Format(time.RFC3339),
				LastActivity:  info.LastActivity.Format(time.RFC3339),
				TurnCount:     info.TurnCount,
				InputTokens:   info.TotalUsage.InputTokens,
				OutputTokens:  info.TotalUsage.OutputTokens,
				TotalTokens:   info.TotalUsage.TotalTokens,
			SessionID:     info.SessionID,
			LastError:     info.LastError,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
	}

	http.Error(w, "issue not found", http.StatusNotFound)
}

// refreshResponse is the JSON response for the /api/v1/refresh endpoint.
type refreshResponse struct {
	Status string `json:"status"`
}

// handleRefresh triggers an immediate orchestrator poll cycle.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.isLeader() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(refreshResponse{Status: "not_leader"})
		return
	}

	s.refresh.ForceRefresh(r.Context())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(refreshResponse{Status: "ok"})
}

// handleEvents is an SSE endpoint that sends state updates.
// It sends an initial state immediately, then polls every 3 seconds.
// The X-Accel-Buffering header disables proxy buffering for SSE compatibility.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Send initial state immediately.
	s.sendStateEvent(w, flusher)

	for {
		select {
		case <-r.Context().Done():
			slog.Debug("httpserver: SSE client disconnected")
			return
		case <-ticker.C:
			s.sendStateEvent(w, flusher)
		}
	}
}

// sendStateEvent writes a single SSE event with the current state.
func (s *Server) sendStateEvent(w http.ResponseWriter, flusher http.Flusher) {
	event := s.buildStateResponse()

	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("httpserver: marshal SSE event", "error", err)
		return
	}

	fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
	flusher.Flush()
}

// leaderAddr returns the leader address from the elector, or empty string.
func leaderAddr(elector interface{ LeaderAddr() string }) string {
	if elector == nil {
		return ""
	}
	return elector.LeaderAddr()
}

// buildStateResponse creates a stateResponse from the current provider state.
func (s *Server) buildStateResponse() stateResponse {
	running := s.state.Running()

	runningJSON := make(map[string]runInfoJSON, len(running))
	for id, info := range running {
		labels := info.Issue.Labels
		if labels == nil {
			labels = []string{}
		}
		runningJSON[id] = runInfoJSON{
			IssueID:       info.Issue.ID,
			Identifier:    info.Issue.Identifier,
			Title:         info.Issue.Title,
			State:         info.Issue.State,
			Labels:        labels,
			URL:           info.Issue.URL,
			WorkerHost:    info.WorkerHost,
			WorkspacePath: info.WorkspacePath,
			Attempt:       info.Attempt,
			StartedAt:     info.StartedAt.Format(time.RFC3339),
			LastActivity:  info.LastActivity.Format(time.RFC3339),
			TurnCount:     info.TurnCount,
			InputTokens:   info.TotalUsage.InputTokens,
			OutputTokens:  info.TotalUsage.OutputTokens,
			TotalTokens:   info.TotalUsage.TotalTokens,
			SessionID:     info.SessionID,
			LastError:     info.LastError,
		}
	}

	// Build retry queue.
	var retryQueue []retryEntryJSON
	pendingRetries := s.state.PendingRetries()
	for issueID, entry := range pendingRetries {
		retryQueue = append(retryQueue, retryEntryJSON{
			IssueID:    issueID,
			Identifier: entry.Issue.Identifier,
			Attempt:    entry.Attempt,
			FireAt:     entry.FireAt.Format(time.RFC3339),
			IsContinue: entry.IsContinue,
		})
	}

	return stateResponse{
		Leader:       s.isLeader(),
		LeaderAddr:   leaderAddr(s.elector),
		RunningCount: s.state.RunningCount(),
		RetryCount:   s.state.RetryCount(),
		TotalRuntimeSeconds: s.state.TotalRuntimeSeconds(),
		RateLimits:          s.state.RateLimits(),
		Running:      runningJSON,
		RetryQueue:   retryQueue,
	}
}
