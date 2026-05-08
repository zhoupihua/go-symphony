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
	Leader       bool                   `json:"leader"`
	LeaderAddr   string                 `json:"leader_addr,omitempty"`
	RunningCount int                    `json:"running_count"`
	Running      map[string]runInfoJSON `json:"running"`
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
}

// handleState returns the current orchestrator state as JSON.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := s.buildStateResponse()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("httpserver: encode state response", "error", err)
	}
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
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(refreshResponse{Status: "ok"})
}

// handleEvents is an SSE endpoint that sends periodic state updates.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(5 * time.Second)
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
		}
	}

	return stateResponse{
		Leader:       s.isLeader(),
		LeaderAddr:   leaderAddr(s.elector),
		RunningCount: s.state.RunningCount(),
		Running:      runningJSON,
	}
}
