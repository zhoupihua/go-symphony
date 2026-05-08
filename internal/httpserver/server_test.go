package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhoupihua/go-symphony/internal/agent"
	"github.com/zhoupihua/go-symphony/internal/config"
	"github.com/zhoupihua/go-symphony/internal/ha"
	"github.com/zhoupihua/go-symphony/internal/orchestrator"
	"github.com/zhoupihua/go-symphony/internal/tracker"
)

// mockStateProvider implements StateProvider for testing.
type mockStateProvider struct {
	running      map[string]*orchestrator.RunInfo
	runningCount int
}

func (m *mockStateProvider) Running() map[string]*orchestrator.RunInfo {
	if m.running != nil {
		return m.running
	}
	return map[string]*orchestrator.RunInfo{}
}

func (m *mockStateProvider) RunningCount() int {
	return m.runningCount
}

func (m *mockStateProvider) RunningIssue(issueID string) *orchestrator.RunInfo {
	if m.running != nil {
		return m.running[issueID]
	}
	return nil
}

func (m *mockStateProvider) PendingRetries() map[string]*orchestrator.RetryEntry {
	return map[string]*orchestrator.RetryEntry{}
}

func (m *mockStateProvider) RetryCount() int {
	return 0
}

func (m *mockStateProvider) TotalRuntimeSeconds() float64 {
	return 0
}

func (m *mockStateProvider) RateLimits() map[string]any {
	return nil
}

// mockRefresher implements Refresher for testing.
type mockRefresher struct {
	called bool
}

func (m *mockRefresher) ForceRefresh(_ context.Context) {
	m.called = true
}

// mockElector implements ha.Elector for testing.
type mockElector struct {
	leader bool
	addr   string
	done   chan struct{}
}

func (m *mockElector) Campaign(_ context.Context) error { return nil }
func (m *mockElector) IsLeader() bool                   { return m.leader }
func (m *mockElector) Resign()                          {}
func (m *mockElector) Done() <-chan struct{}             { return m.done }
func (m *mockElector) LeaderAddr() string               { return m.addr }

func newMockElector(leader bool) *mockElector {
	return &mockElector{
		leader: leader,
		addr:   "localhost:8080",
		done:   make(chan struct{}),
	}
}

// newTestServer creates a Server with mock dependencies for testing.
func newTestServer(state StateProvider, elector ha.Elector, refresher Refresher) *Server {
	cfg := config.ServerConfig{Port: 0, Host: "127.0.0.1"}
	return New(state, elector, refresher, cfg, 10)
}

func TestHealthz(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("expected body 'ok', got %q", string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
}

func TestStateEndpoint(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	state := &mockStateProvider{
		running: map[string]*orchestrator.RunInfo{
			"issue-1": {
				Issue: tracker.Issue{
					ID:         "issue-1",
					Identifier: "ENG-1",
					Title:      "Test issue",
					State:      "In Progress",
					Labels:     []string{"bug"},
					URL:        "https://example.com/issue-1",
				},
				WorkerHost:    "worker-1",
				WorkspacePath: "/tmp/ws-eng-1",
				Attempt:       1,
				StartedAt:     now,
				LastActivity:  now,
				TurnCount:     3,
				TotalUsage:    agent.UsageReport{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
			},
		},
		runningCount: 1,
	}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json content type, got %q", ct)
	}

	var body stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !body.Leader {
		t.Error("expected leader=true")
	}
	if body.RunningCount != 1 {
		t.Errorf("expected running_count=1, got %d", body.RunningCount)
	}
	info, ok := body.Running["issue-1"]
	if !ok {
		t.Fatal("expected issue-1 in running map")
	}
	if info.Identifier != "ENG-1" {
		t.Errorf("expected identifier=ENG-1, got %q", info.Identifier)
	}
	if info.Title != "Test issue" {
		t.Errorf("expected title='Test issue', got %q", info.Title)
	}
	if info.State != "In Progress" {
		t.Errorf("expected state='In Progress', got %q", info.State)
	}
	if info.WorkerHost != "worker-1" {
		t.Errorf("expected worker_host='worker-1', got %q", info.WorkerHost)
	}
	if info.Attempt != 1 {
		t.Errorf("expected attempt=1, got %d", info.Attempt)
	}
	if info.TurnCount != 3 {
		t.Errorf("expected turn_count=3, got %d", info.TurnCount)
	}
	if info.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", info.InputTokens)
	}
	if info.OutputTokens != 200 {
		t.Errorf("expected output_tokens=200, got %d", info.OutputTokens)
	}
	if info.TotalTokens != 300 {
		t.Errorf("expected total_tokens=300, got %d", info.TotalTokens)
	}
}

func TestStateEndpointEmpty(t *testing.T) {
	state := &mockStateProvider{
		running:      map[string]*orchestrator.RunInfo{},
		runningCount: 0,
	}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	var body stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.RunningCount != 0 {
		t.Errorf("expected running_count=0, got %d", body.RunningCount)
	}
	if len(body.Running) != 0 {
		t.Errorf("expected empty running map, got %d entries", len(body.Running))
	}
}

func TestRefreshEndpoint(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", resp.StatusCode)
	}

	if !refresh.called {
		t.Error("expected ForceRefresh to be called")
	}

	var body refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status='ok', got %q", body.Status)
	}
}

func TestRefreshEndpointNotLeader(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(false)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}

	if refresh.called {
		t.Error("expected ForceRefresh NOT to be called when not leader")
	}

	var body refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Status != "not_leader" {
		t.Errorf("expected status='not_leader', got %q", body.Status)
	}
}

func TestMiddlewareCORS(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	// Test CORS on the state endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	origin := resp.Header.Get("Access-Control-Allow-Origin")
	if origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin=*, got %q", origin)
	}

	methods := resp.Header.Get("Access-Control-Allow-Methods")
	if methods != "GET, POST, DELETE, OPTIONS" {
		t.Errorf("expected Access-Control-Allow-Methods='GET, POST, DELETE, OPTIONS', got %q", methods)
	}

	headers := resp.Header.Get("Access-Control-Allow-Headers")
	if headers != "Content-Type" {
		t.Errorf("expected Access-Control-Allow-Headers='Content-Type', got %q", headers)
	}
}

func TestMiddlewareCORSPreflight(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/state", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204 for preflight, got %d", resp.StatusCode)
	}
}

func TestHealthzNoCORS(t *testing.T) {
	// The healthz endpoint should NOT have CORS headers (no cors wrapper).
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	origin := resp.Header.Get("Access-Control-Allow-Origin")
	if origin != "" {
		t.Errorf("expected no CORS headers on /healthz, got Access-Control-Allow-Origin=%q", origin)
	}
}

func TestStateEndpointNilLabels(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	state := &mockStateProvider{
		running: map[string]*orchestrator.RunInfo{
			"issue-2": {
				Issue: tracker.Issue{
					ID:         "issue-2",
					Identifier: "ENG-2",
					Title:      "No labels",
					State:      "Open",
					Labels:     nil, // explicitly nil
				},
				StartedAt:    now,
				LastActivity: now,
			},
		},
		runningCount: 1,
	}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	var body stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	info := body.Running["issue-2"]
	if info.Labels == nil {
		t.Error("expected labels to be empty array, not null")
	}
	if len(info.Labels) != 0 {
		t.Errorf("expected empty labels array, got %v", info.Labels)
	}
}

func TestDashboard(t *testing.T) {
	state := &mockStateProvider{}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html content type, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "<!DOCTYPE html>") {
		t.Error("expected HTML doctype in response")
	}
	if !strings.Contains(bodyStr, "<title>Symphony Dashboard</title>") {
		t.Error("expected dashboard title in response")
	}
	if !strings.Contains(bodyStr, "Symphony") {
		t.Error("dashboard should contain 'Symphony'")
	}
	if !strings.Contains(bodyStr, "LEADER") {
		t.Error("dashboard should contain 'LEADER' badge")
	}
	if !strings.Contains(bodyStr, "htmx") {
		t.Error("dashboard should load htmx")
	}
	if !strings.Contains(bodyStr, "EventSource") {
		t.Error("dashboard should use EventSource for SSE")
	}
	if !strings.Contains(bodyStr, "hx-post=\"/api/v1/refresh\"") {
		t.Error("dashboard should have hx-post refresh button")
	}
	if !strings.Contains(bodyStr, "__maxConcurrent") {
		t.Error("dashboard should inject maxConcurrent into JS")
	}
	if !strings.Contains(bodyStr, "No running issues") {
		t.Error("dashboard should show empty state message when no issues running")
	}
}

func TestDashboardStandbyShowsLeaderAddr(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockElector{leader: false, addr: "leader.example.com:8080", done: make(chan struct{})}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "leader.example.com:8080") {
		t.Error("expected leader address in response when standby")
	}
}

func TestDashboardRunningIssues(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	state := &mockStateProvider{
		running: map[string]*orchestrator.RunInfo{
			"issue-1": {
				Issue: tracker.Issue{
					ID:         "issue-1",
					Identifier: "ENG-42",
					Title:      "Fix memory leak",
					State:      "In Progress",
					Labels:     []string{"bug"},
				},
				WorkspacePath: "/tmp/ws-eng-42",
				Attempt:       2,
				StartedAt:     now,
				LastActivity:  now,
				TurnCount:     5,
				TotalUsage:    agent.UsageReport{InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
			},
		},
		runningCount: 1,
	}
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "ENG-42") {
		t.Error("expected issue identifier ENG-42 in response")
	}
	if !strings.Contains(bodyStr, "Fix memory leak") {
		t.Error("expected issue title in response")
	}
	if !strings.Contains(bodyStr, "In Progress") {
		t.Error("expected issue state in response")
	}
	if !strings.Contains(bodyStr, "/tmp/ws-eng-42") {
		t.Error("expected workspace path in response")
	}
	if !strings.Contains(bodyStr, "Running: <strong>1</strong> / 10") {
		t.Error("expected running count '1 / 10' in response")
	}
}

// mockClusterElector implements both ha.Elector and ha.ClusterManager.
type mockClusterElector struct {
	mockElector
	members   []ha.ClusterMember
	addErr    error
	removeErr error
}

func (m *mockClusterElector) ApplyCommand(_, _ string, _ []byte) error { return nil }
func (m *mockClusterElector) ReplicatedState() ([]byte, error)         { return nil, nil }

func (m *mockClusterElector) AddVoter(_ context.Context, id, addr string) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.members = append(m.members, ha.ClusterMember{ID: id, Address: addr, IsLeader: false})
	return nil
}

func (m *mockClusterElector) RemoveServer(_ context.Context, id string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	for i, member := range m.members {
		if member.ID == id {
			m.members = append(m.members[:i], m.members[i+1:]...)
			break
		}
	}
	return nil
}

func (m *mockClusterElector) GetConfiguration() ([]ha.ClusterMember, error) {
	return m.members, nil
}

func TestClusterGet(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: true, addr: "localhost:8080", done: make(chan struct{})},
		members: []ha.ClusterMember{
			{ID: "node1", Address: "127.0.0.1:9001", IsLeader: true},
			{ID: "node2", Address: "127.0.0.1:9002", IsLeader: false},
		},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var members []clusterMemberJSON
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}
	if members[0].ID != "node1" {
		t.Errorf("expected first member ID=node1, got %q", members[0].ID)
	}
	if !members[0].IsLeader {
		t.Error("expected first member to be leader")
	}
}

func TestClusterAddVoter(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: true, addr: "localhost:8080", done: make(chan struct{})},
		members:     []ha.ClusterMember{{ID: "node1", Address: "127.0.0.1:9001", IsLeader: true}},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	body := `{"id":"node3","address":"127.0.0.1:9003"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/voters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", resp.StatusCode)
	}

	// Verify member was added.
	if len(elector.members) != 2 {
		t.Errorf("expected 2 members after add, got %d", len(elector.members))
	}
}

func TestClusterAddVoterNotLeader(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: false, addr: "", done: make(chan struct{})},
		members:     []ha.ClusterMember{},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	body := `{"id":"node3","address":"127.0.0.1:9003"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/voters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}
}

func TestClusterAddVoterMissingFields(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: true, addr: "localhost:8080", done: make(chan struct{})},
		members:     []ha.ClusterMember{},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	body := `{"id":"node3"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/voters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestClusterRemoveServer(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: true, addr: "localhost:8080", done: make(chan struct{})},
		members: []ha.ClusterMember{
			{ID: "node1", Address: "127.0.0.1:9001", IsLeader: true},
			{ID: "node2", Address: "127.0.0.1:9002", IsLeader: false},
		},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cluster/servers/node2", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", resp.StatusCode)
	}

	// Verify member was removed.
	if len(elector.members) != 1 {
		t.Errorf("expected 1 member after remove, got %d", len(elector.members))
	}
}

func TestClusterRemoveServerNotLeader(t *testing.T) {
	state := &mockStateProvider{}
	elector := &mockClusterElector{
		mockElector: mockElector{leader: false, addr: "", done: make(chan struct{})},
		members:     []ha.ClusterMember{{ID: "node1", Address: "127.0.0.1:9001", IsLeader: true}},
	}
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cluster/servers/node1", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}
}

func TestClusterEndpointsNotRegisteredWithoutHA(t *testing.T) {
	state := &mockStateProvider{}
	// Regular mockElector does NOT implement ClusterManager.
	elector := newMockElector(true)
	refresh := &mockRefresher{}
	srv := newTestServer(state, elector, refresh)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster", nil)
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404 when HA not enabled, got %d", resp.StatusCode)
	}
}
