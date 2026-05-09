package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/zhoupihua/go-symphony/internal/orchestrator"
)

// handleHealthz returns a simple health check response.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// errorDetail is the nested error object per SPEC §13.7.1.
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorResponse is a JSON error envelope for API responses per SPEC §13.7.1.
type errorResponse struct {
	Error errorDetail `json:"error"`
}

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	writeJSONErrorCode(w, code, http.StatusText(code), message)
}

// writeJSONErrorCode writes a JSON error response with a specific error code.
func writeJSONErrorCode(w http.ResponseWriter, code int, errCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorResponse{Error: errorDetail{Code: errCode, Message: message}})
}

// stateResponse is the JSON response for the /api/v1/state endpoint.
type stateResponse struct {
	Leader       bool                   `json:"leader"`
	LeaderAddr   string                 `json:"leader_addr,omitempty"`
	GeneratedAt  string                 `json:"generated_at"`
	Counts       stateCounts            `json:"counts"`
	CodexTotals  codexTotalsJSON        `json:"codex_totals"`
	RateLimits   map[string]any         `json:"rate_limits"`
	Running      []runInfoJSON          `json:"running"`
	Retrying     []retryEntryJSON       `json:"retrying,omitempty"`
}

type stateCounts struct {
	Running  int `json:"running"`
	Retrying int `json:"retrying"`
}

type codexTotalsJSON struct {
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	SecondsRunning float64 `json:"seconds_running"`
}

// runInfoJSON is the JSON representation of an orchestrator.RunInfo for the API.
type runInfoJSON struct {
	IssueID       string   `json:"issue_id"`
	Identifier    string   `json:"identifier"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	Labels        []string `json:"labels"`
	URL           string   `json:"url"`
	BranchName    string   `json:"branch_name"`
	WorkerHost    string   `json:"worker_host"`
	WorkspacePath string   `json:"workspace_path"`
	Attempt       int      `json:"attempt"`
	Phase         string   `json:"phase"`
	StartedAt     string   `json:"started_at"`
	LastActivity  string   `json:"last_activity"`
	TurnCount     int      `json:"turn_count"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	TotalTokens   int64    `json:"total_tokens"`
	SessionID     string   `json:"session_id,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	ThreadID      string   `json:"thread_id,omitempty"`
	TurnID        string   `json:"turn_id,omitempty"`
	LastCodexEvent string  `json:"last_codex_event,omitempty"`
	LastCodexTS   string   `json:"last_codex_ts,omitempty"`
	LastCodexMsg  string   `json:"last_codex_message,omitempty"`
	CodexServerPID string  `json:"codex_server_pid,omitempty"`
}

// retryEntryJSON is the JSON representation of an orchestrator.RetryEntry.
type retryEntryJSON struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Attempt    int    `json:"attempt"`
	DueAt      string `json:"due_at"`
	IsContinue bool   `json:"is_continue"`
	Error      string `json:"error,omitempty"`
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
		writeJSONError(w, http.StatusBadRequest, "missing identifier")
		return
	}

	// Search through running issues for the matching identifier.
	running := s.state.Running()
	for _, info := range running {
		if info.Issue.Identifier == identifier {
			resp := s.buildIssueDetail(info)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
	}

	// Check retry queue.
	pendingRetries := s.state.PendingRetries()
	for _, entry := range pendingRetries {
		if entry.Issue.Identifier == identifier {
			resp := issueDetailResponse{
				IssueIdentifier: entry.Issue.Identifier,
				IssueID:         entry.Issue.ID,
				Status:          "retrying",
				Retry: &retryDetailJSON{
					Attempt:    entry.Attempt,
					DueAt:      entry.FireAt.Format(time.RFC3339),
					IsContinue: entry.IsContinue,
					Error:      entry.Error,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
	}

	writeJSONErrorCode(w, http.StatusNotFound, "issue_not_found", "issue not found in current state")
}

// issueDetailResponse is the enriched per-issue response per SPEC S13.7.2.
type issueDetailResponse struct {
	IssueIdentifier string           `json:"issue_identifier"`
	IssueID         string           `json:"issue_id"`
	Status          string           `json:"status"`
	Workspace       *workspaceDetail  `json:"workspace,omitempty"`
	Attempts        *attemptsDetail   `json:"attempts,omitempty"`
	Running         *runningDetail    `json:"running,omitempty"`
	Retry           *retryDetailJSON  `json:"retry,omitempty"`
	LastError       string           `json:"last_error,omitempty"`
}

type workspaceDetail struct {
	Path string `json:"path"`
}

type attemptsDetail struct {
	CurrentAttempt int `json:"current_attempt"`
}

type runningDetail struct {
	SessionID      string      `json:"session_id"`
	TurnCount      int         `json:"turn_count"`
	State          string      `json:"state"`
	StartedAt      string      `json:"started_at"`
	LastEvent      string      `json:"last_event,omitempty"`
	LastMessage    string      `json:"last_message,omitempty"`
	LastEventAt    string      `json:"last_event_at,omitempty"`
	ThreadID       string      `json:"thread_id,omitempty"`
	TurnID         string      `json:"turn_id,omitempty"`
	CodexServerPID string      `json:"codex_server_pid,omitempty"`
	Tokens         tokenDetail `json:"tokens"`
}

type tokenDetail struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type retryDetailJSON struct {
	Attempt    int    `json:"attempt"`
	DueAt      string `json:"due_at"`
	IsContinue bool   `json:"is_continue"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) buildIssueDetail(info *orchestrator.RunInfo) issueDetailResponse {
	return issueDetailResponse{
		IssueIdentifier: info.Issue.Identifier,
		IssueID:         info.Issue.ID,
		Status:          "running",
		Workspace: &workspaceDetail{
			Path: info.WorkspacePath,
		},
		Attempts: &attemptsDetail{
			CurrentAttempt: info.Attempt,
		},
		Running: &runningDetail{
			SessionID:      info.SessionID,
			TurnCount:      info.TurnCount,
			State:          info.Issue.State,
			StartedAt:      info.StartedAt.Format(time.RFC3339),
			LastEvent:      info.LastCodexEvent,
			LastMessage:    info.LastCodexMsg,
			LastEventAt:    info.LastCodexTS.Format(time.RFC3339),
			ThreadID:       info.ThreadID,
			TurnID:         info.TurnID,
			CodexServerPID: info.CodexServerPID,
			Tokens: tokenDetail{
				InputTokens:  info.TotalUsage.InputTokens,
				OutputTokens: info.TotalUsage.OutputTokens,
				TotalTokens:  info.TotalUsage.TotalTokens,
			},
		},
		LastError: info.LastError,
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
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(refreshResponse{Status: "ok"})
}

// handleEvents is an SSE endpoint that sends state updates.
// It sends an initial state immediately, then polls every 3 seconds.
// The X-Accel-Buffering header disables proxy buffering for SSE compatibility.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
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

	var runningJSON []runInfoJSON
	for _, info := range running {
		labels := info.Issue.Labels
		if labels == nil {
			labels = []string{}
		}
		runningJSON = append(runningJSON, runInfoJSON{
			IssueID:       info.Issue.ID,
			Identifier:    info.Issue.Identifier,
			Title:         info.Issue.Title,
			State:         info.Issue.State,
			Labels:        labels,
			URL:           info.Issue.URL,
			BranchName:    info.Issue.BranchName,
			WorkerHost:    info.WorkerHost,
			WorkspacePath: info.WorkspacePath,
			Attempt:       info.Attempt,
			Phase:         info.Phase,
			StartedAt:     info.StartedAt.Format(time.RFC3339),
			LastActivity:  info.LastActivity.Format(time.RFC3339),
			TurnCount:     info.TurnCount,
			InputTokens:   info.TotalUsage.InputTokens,
			OutputTokens:  info.TotalUsage.OutputTokens,
			TotalTokens:   info.TotalUsage.TotalTokens,
			SessionID:      info.SessionID,
			LastError:      info.LastError,
				ThreadID:       info.ThreadID,
				TurnID:         info.TurnID,
				LastCodexEvent: info.LastCodexEvent,
				LastCodexTS:    info.LastCodexTS.Format(time.RFC3339),
				LastCodexMsg:   info.LastCodexMsg,
				CodexServerPID: info.CodexServerPID,
		})
	}

	// Build retry queue.
	var retryingJSON []retryEntryJSON
	pendingRetries := s.state.PendingRetries()
	for issueID, entry := range pendingRetries {
		retryingJSON = append(retryingJSON, retryEntryJSON{
			IssueID:    issueID,
			Identifier: entry.Issue.Identifier,
			Attempt:    entry.Attempt,
			DueAt:      entry.FireAt.Format(time.RFC3339),
			IsContinue: entry.IsContinue,
		Error:      entry.Error,
		})
	}

	inputTokens, outputTokens, totalTokens, secondsRunning := s.state.CodexTotals()

	return stateResponse{
		Leader:      s.isLeader(),
		LeaderAddr:  leaderAddr(s.elector),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Counts: stateCounts{
			Running:  s.state.RunningCount(),
			Retrying: s.state.RetryCount(),
		},
		CodexTotals: codexTotalsJSON{
			InputTokens:    inputTokens,
			OutputTokens:   outputTokens,
			TotalTokens:    totalTokens,
			SecondsRunning: secondsRunning,
		},
		RateLimits: s.state.RateLimits(),
		Running:    runningJSON,
		Retrying:    retryingJSON,
	}
}

// clusterMemberJSON is the JSON representation of a cluster member.
type clusterMemberJSON struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	IsLeader bool   `json:"is_leader"`
}

// addVoterRequest is the JSON request for adding a voter.
type addVoterRequest struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

// handleClusterGet returns the current cluster membership.
func (s *Server) handleClusterGet(w http.ResponseWriter, r *http.Request) {
	if s.clusterManager == nil {
		writeJSONError(w, http.StatusNotFound, "cluster management not available")
		return
	}

	members, err := s.clusterManager.GetConfiguration()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get cluster configuration")
		return
	}

	resp := make([]clusterMemberJSON, len(members))
	for i, m := range members {
		resp[i] = clusterMemberJSON{ID: m.ID, Address: m.Address, IsLeader: m.IsLeader}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleClusterAddVoter adds a new voter to the cluster.
func (s *Server) handleClusterAddVoter(w http.ResponseWriter, r *http.Request) {
	if s.clusterManager == nil {
		writeJSONError(w, http.StatusNotFound, "cluster management not available")
		return
	}

	if !s.isLeader() {
		writeJSONError(w, http.StatusServiceUnavailable, "not leader")
		return
	}

	var req addVoterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ID == "" || req.Address == "" {
		writeJSONError(w, http.StatusBadRequest, "id and address are required")
		return
	}

	if err := s.clusterManager.AddVoter(r.Context(), req.ID, req.Address); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// handleClusterRemoveServer removes a server from the cluster.
func (s *Server) handleClusterRemoveServer(w http.ResponseWriter, r *http.Request) {
	if s.clusterManager == nil {
		writeJSONError(w, http.StatusNotFound, "cluster management not available")
		return
	}

	if !s.isLeader() {
		writeJSONError(w, http.StatusServiceUnavailable, "not leader")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "missing server id")
		return
	}

	if err := s.clusterManager.RemoveServer(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}
