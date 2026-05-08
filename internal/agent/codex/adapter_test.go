package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/tracker"
)

// Compile-time interface checks.
var (
	_ agent.Agent   = (*CodexAdapter)(nil)
	_ agent.Session = (*CodexSession)(nil)
)

// scriptServer reads JSON-RPC requests from r and writes scripted responses to w.
// For each incoming request, it looks up the method in its script and sends the
// configured messages in order. Crucially, it does NOT mirror request IDs onto
// notifications — only responses (which already have their own ID) get sent as-is,
// and notifications (no ID) stay as notifications.
type scriptServer struct {
	mu     sync.Mutex
	script map[string][]*Message
}

func newScriptServer(script map[string][]*Message) *scriptServer {
	return &scriptServer{script: script}
}

func (s *scriptServer) run(r io.Reader, w io.Writer, done chan<- struct{}) {
	defer close(done)
	dec := NewDecoder(r)
	enc := NewEncoder(w)

	for {
		msg, err := dec.Decode()
		if err != nil {
			return
		}

		s.mu.Lock()
		toSend := s.script[msg.Method]
		s.mu.Unlock()

		for _, m := range toSend {
			// Only mirror request ID for responses that don't have one already.
			// Notifications (no ID, has Method) must stay as notifications.
			if m.ID == nil && m.Method == "" && msg.ID != nil {
				// This is a response without an ID — mirror the request ID.
				idCopy := make(json.RawMessage, len(*msg.ID))
				copy(idCopy, *msg.ID)
				m.ID = &idCopy
			}
			if err := enc.Encode(m); err != nil {
				return
			}
		}
	}
}

// newTestServer is a simpler approach: creates pipes, starts the mock server,
// performs the handshake, and returns a ready CodexSession.
func newTestServer(t *testing.T, turnMessages ...*Message) *CodexSession {
	t.Helper()

	initResp, _ := Response(1, map[string]any{"capabilities": map[string]any{}})
	threadResp, _ := Response(2, map[string]any{"threadId": "thread-test"})

	script := map[string][]*Message{
		MethodInitialize:  {initResp},
		MethodThreadStart: {threadResp},
	}
	if len(turnMessages) > 0 {
		script[MethodTurnStart] = turnMessages
	}

	server := newScriptServer(script)

	adapterRead, serverWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	serverRead, adapterWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	done := make(chan struct{})
	go server.run(serverRead, serverWrite, done)

	cmd := exec.CommandContext(context.Background(), "sleep", "infinity")

	sess := &CodexSession{
		cmd:            cmd,
		stdin:          &osFileWriter{adapterWrite},
		enc:            NewEncoder(adapterWrite),
		dec:            NewDecoder(adapterRead),
		opts:           agent.SessionOptions{WorkspacePath: "/tmp/test"},
		turnTimeout:    30 * time.Second,
		readTimeout:    5 * time.Second,
		stallTimeout:   2 * time.Minute,
		approvalPolicy: "auto",
	}

	// Handshake: initialize
	initReq, _ := Request(1, MethodInitialize, InitializeParams{
		Capabilities: map[string]any{},
		ClientInfo:   ClientInfo{Name: "symphony", Version: "0.1.0"},
	})
	if err := sess.enc.Encode(initReq); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	initRespMsg, err := decodeWithTimeout(sess.dec, 5*time.Second)
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if initRespMsg.Error != nil {
		t.Fatalf("initialize error: %s", initRespMsg.Error.Message)
	}

	// Handshake: initialized notification
	initNotif, _ := Notification(MethodInitialized, InitializedParams{})
	if err := sess.enc.Encode(initNotif); err != nil {
		t.Fatalf("send initialized: %v", err)
	}

	// Handshake: thread/start
	threadReq, _ := Request(2, MethodThreadStart, ThreadStartParams{
		ApprovalPolicy: "auto",
		CWD:            "/tmp/test",
	})
	if err := sess.enc.Encode(threadReq); err != nil {
		t.Fatalf("send thread/start: %v", err)
	}
	threadRespMsg, err := decodeWithTimeout(sess.dec, 5*time.Second)
	if err != nil {
		t.Fatalf("read thread/start response: %v", err)
	}
	if threadRespMsg.Error != nil {
		t.Fatalf("thread/start error: %s", threadRespMsg.Error.Message)
	}

	var threadResult struct {
		ThreadID string `json:"threadId"`
	}
	if len(threadRespMsg.Result) > 0 {
		_ = json.Unmarshal(threadRespMsg.Result, &threadResult)
	}
	sess.threadID = threadResult.ThreadID

	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}

	return sess
}

// osFileWriter wraps an *os.File to implement io.WriteCloser.
type osFileWriter struct {
	*os.File
}

// --- Tests ---

func TestAdapterNewAdapter(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantCmd string
		wantPol string
	}{
		{
			name:    "defaults",
			cfg:     map[string]any{},
			wantCmd: "codex app-server",
			wantPol: "auto",
		},
		{
			name: "custom command and policy",
			cfg: map[string]any{
				"command":         "codex app-server --model gpt-4",
				"approval_policy": "suggest",
			},
			wantCmd: "codex app-server --model gpt-4",
			wantPol: "suggest",
		},
		{
			name: "custom timeouts",
			cfg: map[string]any{
				"turn_timeout_ms":  60000,
				"read_timeout_ms":  10000,
				"stall_timeout_ms": 180000,
			},
			wantCmd: "codex app-server",
			wantPol: "auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewAdapter(tt.cfg)
			if err != nil {
				t.Fatalf("NewAdapter() error = %v", err)
			}
			ca := a.(*CodexAdapter)
			if ca.command != tt.wantCmd {
				t.Errorf("command = %q, want %q", ca.command, tt.wantCmd)
			}
			if ca.approvalPolicy != tt.wantPol {
				t.Errorf("approvalPolicy = %q, want %q", ca.approvalPolicy, tt.wantPol)
			}
		})
	}
}

func TestAdapterStartSessionHandshake(t *testing.T) {
	sess := newTestServer(t)
	defer sess.Close()

	if sess.threadID != "thread-test" {
		t.Errorf("threadID = %q, want %q", sess.threadID, "thread-test")
	}
}

func TestAdapterRunTurnCompleted(t *testing.T) {
	turnAck, _ := Response(101, map[string]any{"status": "started"})
	completedNotif, _ := Notification(MethodTurnCompleted, map[string]any{
		"usage": map[string]any{
			"inputTokens":  100,
			"outputTokens": 200,
			"totalTokens":  300,
		},
		"output": "Task completed successfully",
	})

	sess := newTestServer(t, turnAck, completedNotif)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := sess.RunTurn(ctx, "Fix the bug", agent.TurnOptions{})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
	if result.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", result.Usage.OutputTokens)
	}
	if result.Output != "Task completed successfully" {
		t.Errorf("Output = %q, want %q", result.Output, "Task completed successfully")
	}
}

func TestAdapterRunTurnFailed(t *testing.T) {
	turnAck, _ := Response(101, map[string]any{"status": "started"})
	failedNotif, _ := Notification(MethodTurnFailed, map[string]any{
		"error": "model overloaded",
	})

	sess := newTestServer(t, turnAck, failedNotif)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := sess.RunTurn(ctx, "Fix the bug", agent.TurnOptions{})
	if err == nil {
		t.Fatal("RunTurn() should return error for turn/failed")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("error = %q, want containing 'model overloaded'", err.Error())
	}
}

func TestAdapterRunTurnCancelled(t *testing.T) {
	turnAck, _ := Response(101, map[string]any{"status": "started"})
	cancelledNotif, _ := Notification(MethodTurnCancelled, nil)

	sess := newTestServer(t, turnAck, cancelledNotif)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := sess.RunTurn(ctx, "Fix the bug", agent.TurnOptions{})
	if err == nil {
		t.Fatal("RunTurn() should return error for turn/cancelled")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want containing 'cancelled'", err.Error())
	}
}

func TestAdapterRunTurnApproval(t *testing.T) {
	turnAck, _ := Response(101, map[string]any{"status": "started"})
	approvalReq, _ := Request(10, MethodItemCommandApproval, map[string]any{
		"command": "rm -rf /tmp/test",
	})
	completedNotif, _ := Notification(MethodTurnCompleted, map[string]any{
		"usage": map[string]any{
			"inputTokens":  50,
			"outputTokens": 75,
			"totalTokens":  125,
		},
	})

	sess := newTestServer(t, turnAck, approvalReq, completedNotif)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := sess.RunTurn(ctx, "Fix the bug", agent.TurnOptions{})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
}

func TestAdapterClose(t *testing.T) {
	sess := newTestServer(t)
	err := sess.Close()
	// On Windows, "sleep infinity" may not exit gracefully, so we accept
	// either a clean exit or the "process did not exit gracefully" error.
	if err != nil && !strings.Contains(err.Error(), "did not exit gracefully") {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestAdapterContextCancellation(t *testing.T) {
	// Only send the ack, no completion — the turn will hang until context cancels.
	turnAck, _ := Response(101, map[string]any{"status": "started"})

	sess := newTestServer(t, turnAck)
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := sess.RunTurn(ctx, "Fix the bug", agent.TurnOptions{})
	if err == nil {
		t.Fatal("RunTurn() should return error when context is cancelled")
	}
}

func TestAdapterInterfaceCompliance(t *testing.T) {
	var _ agent.Agent = (*CodexAdapter)(nil)
	var _ agent.Session = (*CodexSession)(nil)
}

func TestApprovalAutoPolicy(t *testing.T) {
	var buf mockBuffer
	enc := NewEncoder(&buf)

	req, _ := Request(5, MethodItemCommandApproval, map[string]any{
		"command": "ls -la",
	})

	err := handleApproval("auto", req, enc)
	if err != nil {
		t.Fatalf("handleApproval() error = %v", err)
	}

	dec := NewDecoder(&buf)
	resp, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var decision ApprovalDecision
	if err := json.Unmarshal(resp.Result, &decision); err != nil {
		t.Fatalf("unmarshal decision: %v", err)
	}
	if decision.Decision != "approve" {
		t.Errorf("Decision = %q, want %q", decision.Decision, "approve")
	}
}

func TestApprovalNonAutoPolicy(t *testing.T) {
	var buf mockBuffer
	enc := NewEncoder(&buf)

	req, _ := Request(6, MethodItemCommandApproval, map[string]any{
		"command": "rm -rf /",
	})

	err := handleApproval("suggest", req, enc)
	if err != nil {
		t.Fatalf("handleApproval() error = %v", err)
	}

	dec := NewDecoder(&buf)
	resp, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var decision ApprovalDecision
	if err := json.Unmarshal(resp.Result, &decision); err != nil {
		t.Fatalf("unmarshal decision: %v", err)
	}
	if decision.Decision != "deny" {
		t.Errorf("Decision = %q, want %q", decision.Decision, "deny")
	}
}

func TestHandleToolCall(t *testing.T) {
	var buf mockBuffer
	enc := NewEncoder(&buf)

	req, _ := Request(30, MethodItemToolCall, map[string]any{
		"name":  "linear_graphql",
		"input": map[string]any{"query": "{ issues { id } }"},
	})

	err := handleToolCall(context.Background(), req, enc, &stubTracker{})
	if err != nil {
		t.Fatalf("handleToolCall() error = %v", err)
	}

	dec := NewDecoder(&buf)
	resp, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var result ToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Success {
		t.Error("Success = true, want false (known limitation)")
	}
}

func TestExecuteDynamicTool(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		wantErr bool
		wantOk  bool
	}{
		{"unknown", "unknown_tool", false, false},
		{"linear", "linear_graphql", false, false},
		{"plane", "plane_rest", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executeDynamicTool(context.Background(), tt.tool, nil, &stubTracker{})
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if result.Success != tt.wantOk {
				t.Errorf("Success = %v, want %v", result.Success, tt.wantOk)
			}
		})
	}
}

func TestBuildDynamicTools(t *testing.T) {
	if tools := buildDynamicTools(nil); tools != nil {
		t.Errorf("buildDynamicTools(nil) = %v, want nil", tools)
	}
	if tools := buildDynamicTools(&stubTracker{}); len(tools) != 2 {
		t.Errorf("len(tools) = %d, want 2", len(tools))
	}
}

func TestExtractUsage(t *testing.T) {
	msg, _ := Notification(MethodTurnCompleted, map[string]any{
		"usage": map[string]any{
			"inputTokens":  500,
			"outputTokens": 1000,
			"totalTokens":  1500,
		},
	})

	usage := extractUsage(msg)
	if usage.InputTokens != 500 || usage.OutputTokens != 1000 || usage.TotalTokens != 1500 {
		t.Errorf("usage = %+v, want {500 1000 1500}", usage)
	}
}

func TestExtractUsageEmpty(t *testing.T) {
	msg, _ := Notification(MethodTurnCompleted, nil)
	usage := extractUsage(msg)
	if usage != (agent.UsageReport{}) {
		t.Errorf("usage = %+v, want zero value", usage)
	}
}

func TestAdapterNewAdapterTimeouts(t *testing.T) {
	cfg := map[string]any{
		"turn_timeout_ms":  float64(120000),
		"read_timeout_ms":  float64(15000),
		"stall_timeout_ms": float64(300000),
	}
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	ca := a.(*CodexAdapter)
	if ca.turnTimeout != 120*time.Second {
		t.Errorf("turnTimeout = %v, want %v", ca.turnTimeout, 120*time.Second)
	}
	if ca.readTimeout != 15*time.Second {
		t.Errorf("readTimeout = %v, want %v", ca.readTimeout, 15*time.Second)
	}
	if ca.stallTimeout != 5*time.Minute {
		t.Errorf("stallTimeout = %v, want %v", ca.stallTimeout, 5*time.Minute)
	}
}

// stubTracker implements tracker.Tracker with no-op methods.
type stubTracker struct{}

func (s *stubTracker) FetchCandidateIssues(_ context.Context) ([]tracker.Issue, error) {
	return nil, nil
}
func (s *stubTracker) FetchIssuesByStates(_ context.Context, _ []string) ([]tracker.Issue, error) {
	return nil, nil
}
func (s *stubTracker) FetchIssueStatesByIDs(_ context.Context, _ []string) ([]tracker.Issue, error) {
	return nil, nil
}
func (s *stubTracker) CreateComment(_ context.Context, _, _ string) error { return nil }
func (s *stubTracker) UpdateIssueState(_ context.Context, _, _ string) error {
	return nil
}
func (s *stubTracker) RawClient() any { return nil }

// mockBuffer is a simple read/write buffer for tests.
type mockBuffer struct {
	data []byte
	pos  int
}

func (m *mockBuffer) Read(p []byte) (int, error) {
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockBuffer) Write(p []byte) (int, error) {
	m.data = append(m.data, p...)
	return len(p), nil
}

func (m *mockBuffer) String() string { return string(m.data) }

var _ fmt.Stringer = (*mockBuffer)(nil)
