package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/ainative/go-symphony/internal/agent"
	"github.com/ainative/go-symphony/internal/tracker"
)

// Compile-time interface checks.
var (
	_ agent.Agent   = (*CodexAdapter)(nil)
	_ agent.Session = (*CodexSession)(nil)
)

// CodexAdapter implements agent.Agent for the Codex CLI (codex app-server).
type CodexAdapter struct {
	command        string
	approvalPolicy string
	sandbox        string
	turnTimeout    time.Duration
	readTimeout    time.Duration
	stallTimeout   time.Duration
}

// NewAdapter creates a CodexAdapter from adapter-specific config.
func NewAdapter(cfg map[string]any) (agent.Agent, error) {
	a := &CodexAdapter{
		command:        "codex app-server",
		approvalPolicy: "auto",
		readTimeout:    30 * time.Second,
		stallTimeout:   2 * time.Minute,
		turnTimeout:    10 * time.Minute,
	}

	if v, ok := cfg["command"].(string); ok && v != "" {
		a.command = v
	}
	if v, ok := cfg["approval_policy"].(string); ok && v != "" {
		a.approvalPolicy = v
	}
	if v, ok := cfg["thread_sandbox"].(string); ok && v != "" {
		a.sandbox = v
	}
	if v, ok := cfg["turn_timeout_ms"].(float64); ok && v > 0 {
		a.turnTimeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := cfg["read_timeout_ms"].(float64); ok && v > 0 {
		a.readTimeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := cfg["stall_timeout_ms"].(float64); ok && v > 0 {
		a.stallTimeout = time.Duration(v) * time.Millisecond
	}

	return a, nil
}

// Register registers the Codex adapter in the agent registry.
func Register() {
	agent.RegisterAgent("codex", NewAdapter)
}

// StartSession spawns a codex subprocess, performs the initialize and thread/start
// handshake, and returns an active CodexSession.
func (a *CodexAdapter) StartSession(ctx context.Context, opts agent.SessionOptions) (agent.Session, error) {
	// 1. Spawn subprocess.
	cmd := exec.CommandContext(ctx, "bash", "-lc", a.command)
	cmd.Dir = opts.WorkspacePath

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: get stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start process: %w", err)
	}

	enc := NewEncoder(stdinPipe)
	dec := NewDecoder(stdoutPipe)

	// 2. Send initialize request.
	initReq, err := Request(1, MethodInitialize, InitializeParams{
		Capabilities: map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "symphony",
			Version: "0.1.0",
		},
	})
	if err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: build initialize request: %w", err)
	}
	if err := enc.Encode(initReq); err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: send initialize request: %w", err)
	}

	// Wait for initialize response with timeout.
	initResp, err := decodeWithTimeout(dec, a.readTimeout)
	if err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: read initialize response: %w", err)
	}
	if initResp.Error != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: initialize failed: %s", initResp.Error.Message)
	}

	// 3. Send initialized notification.
	initNotif, err := Notification(MethodInitialized, InitializedParams{})
	if err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: build initialized notification: %w", err)
	}
	if err := enc.Encode(initNotif); err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: send initialized notification: %w", err)
	}

	// 4. Build dynamic tools from tracker.
	dynamicTools := buildDynamicTools(opts.Tracker)

	// 5. Send thread/start request.
	threadStartReq, err := Request(2, MethodThreadStart, ThreadStartParams{
		ApprovalPolicy: a.approvalPolicy,
		Sandbox:        a.sandbox,
		CWD:            opts.WorkspacePath,
		DynamicTools:   dynamicTools,
	})
	if err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: build thread/start request: %w", err)
	}
	if err := enc.Encode(threadStartReq); err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: send thread/start request: %w", err)
	}

	// Wait for thread/start response.
	threadResp, err := decodeWithTimeout(dec, a.readTimeout)
	if err != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: read thread/start response: %w", err)
	}
	if threadResp.Error != nil {
		stdinPipe.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("codex: thread/start failed: %s", threadResp.Error.Message)
	}

	// Extract threadId from result.
	var threadResult struct {
		ThreadID string `json:"threadId"`
	}
	if len(threadResp.Result) > 0 {
		if err := json.Unmarshal(threadResp.Result, &threadResult); err != nil {
			stdinPipe.Close()
			cmd.Process.Kill()
			return nil, fmt.Errorf("codex: parse thread/start result: %w", err)
		}
	}

	sess := &CodexSession{
		cmd:           cmd,
		stdin:         stdinPipe,
		enc:           enc,
		dec:           dec,
		threadID:      threadResult.ThreadID,
		opts:          opts,
		turnTimeout:   a.turnTimeout,
		readTimeout:   a.readTimeout,
		stallTimeout:  a.stallTimeout,
		approvalPolicy: a.approvalPolicy,
		sandbox:       a.sandbox,
	}

	return sess, nil
}

// CodexSession implements agent.Session for an active Codex conversation.
type CodexSession struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	enc            *Encoder
	dec            *Decoder
	threadID       string
	opts           agent.SessionOptions
	turnTimeout    time.Duration
	readTimeout    time.Duration
	stallTimeout   time.Duration
	approvalPolicy string
	sandbox        string
	nextID         int
}

// RunTurn sends a turn/start request and processes the message loop until the
// turn completes, fails, or is cancelled.
func (s *CodexSession) RunTurn(ctx context.Context, prompt string, opts agent.TurnOptions) (agent.TurnResult, error) {
	s.nextID++
	turnID := s.nextID + 100 // offset to avoid colliding with handshake IDs

	policy := s.approvalPolicy
	if opts.ApprovalPolicy != "" {
		policy = opts.ApprovalPolicy
	}

	turnReq, err := Request(turnID, MethodTurnStart, TurnStartParams{
		ThreadID:       s.threadID,
		Input:          prompt,
		CWD:            s.opts.WorkspacePath,
		ApprovalPolicy: policy,
		SandboxPolicy:  s.sandbox,
	})
	if err != nil {
		return agent.TurnResult{}, fmt.Errorf("codex: build turn/start request: %w", err)
	}
	if err := s.enc.Encode(turnReq); err != nil {
		return agent.TurnResult{}, fmt.Errorf("codex: send turn/start request: %w", err)
	}

	// Apply turn timeout if specified.
	turnCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	} else if s.turnTimeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, s.turnTimeout)
		defer cancel()
	}

	// Receive loop: handle notifications and wait for turn lifecycle events.
	for {
		// Check context cancellation.
		select {
		case <-turnCtx.Done():
			return agent.TurnResult{}, fmt.Errorf("codex: turn cancelled: %w", turnCtx.Err())
		default:
		}

		msg, err := decodeWithTimeoutCtx(turnCtx, s.dec, s.readTimeout)
		if err != nil {
			return agent.TurnResult{}, fmt.Errorf("codex: decode message: %w", err)
		}

		// Handle notifications from the server.
		if msg.IsNotification() {
			result, handled, err := s.handleNotification(ctx, msg)
			if err != nil {
				return agent.TurnResult{}, err
			}
			if handled {
				return result, nil
			}
			continue
		}

		// Handle responses (acknowledgements to our requests).
		if msg.IsResponse() {
			if msg.Error != nil {
				return agent.TurnResult{}, fmt.Errorf("codex: request error: %s", msg.Error.Message)
			}
			// Turn/start response is just an ack; keep waiting for notifications.
			continue
		}

		// Handle server-initiated requests (e.g., approval requests that have an ID).
		if msg.IsRequest() {
			if err := s.handleServerRequest(ctx, msg); err != nil {
				return agent.TurnResult{}, fmt.Errorf("codex: handle server request: %w", err)
			}
			continue
		}
	}
}

// handleNotification processes a server notification and returns (TurnResult, true)
// if the turn is finished, or (zero, false) to continue the receive loop.
func (s *CodexSession) handleNotification(_ context.Context, msg *Message) (agent.TurnResult, bool, error) {
	switch msg.Method {
	case MethodTurnCompleted:
		usage := extractUsage(msg)
		output := extractOutput(msg)
		return agent.TurnResult{
			Completed: true,
			Usage:     usage,
			Output:    output,
		}, true, nil

	case MethodTurnFailed:
		errMsg := "turn failed"
		if len(msg.Params) > 0 {
			var params struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(msg.Params, &params) == nil && params.Error != "" {
				errMsg = params.Error
			}
		}
		return agent.TurnResult{}, true, fmt.Errorf("codex: %s", errMsg)

	case MethodTurnCancelled:
		return agent.TurnResult{}, true, fmt.Errorf("codex: turn cancelled")

	default:
		// Ignore other notifications.
		return agent.TurnResult{}, false, nil
	}
}

// handleServerRequest processes a server-initiated request (has ID and Method).
func (s *CodexSession) handleServerRequest(ctx context.Context, msg *Message) error {
	switch msg.Method {
	case MethodItemCommandApproval, MethodItemFileChangeApproval,
		MethodExecCommandApproval, MethodApplyPatchApproval:
		return handleApproval(s.approvalPolicy, msg, s.enc)

	case MethodItemToolCall:
		return handleToolCall(ctx, msg, s.enc, s.opts.Tracker)

	case MethodItemToolRequestUserInput:
		return fmt.Errorf("codex: agent requested user input, which is not supported in automated mode")

	default:
		// Unknown server request; respond with a generic acknowledgment.
		var id int
		if msg.ID != nil {
			_ = json.Unmarshal(*msg.ID, &id)
		}
		resp, err := Response(id, map[string]string{"status": "ignored"})
		if err != nil {
			return fmt.Errorf("codex: build response for unknown request: %w", err)
		}
		if err := s.enc.Encode(resp); err != nil {
			return fmt.Errorf("codex: send response for unknown request: %w", err)
		}
		return nil
	}
}

// Close terminates the Codex subprocess.
func (s *CodexSession) Close() error {
	if err := s.stdin.Close(); err != nil {
		return fmt.Errorf("codex: close stdin: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-time.After(5 * time.Second):
		if err := s.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("codex: kill process: %w", err)
		}
		return fmt.Errorf("codex: process did not exit gracefully")
	case err := <-done:
		return err
	}
}

// decodeWithTimeout reads one message with a fixed timeout.
func decodeWithTimeout(dec *Decoder, timeout time.Duration) (*Message, error) {
	// Use a channel-based approach since Decoder is not context-aware.
	type result struct {
		msg *Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := dec.Decode()
		ch <- result{msg: m, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil, fmt.Errorf("read timeout after %v", timeout)
	case r := <-ch:
		return r.msg, r.err
	}
}

// decodeWithTimeoutCtx reads one message, respecting both a context and a
// per-read timeout.
func decodeWithTimeoutCtx(ctx context.Context, dec *Decoder, timeout time.Duration) (*Message, error) {
	type result struct {
		msg *Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := dec.Decode()
		ch <- result{msg: m, err: err}
	}()

	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.msg, r.err
	case <-timer.C:
		return nil, fmt.Errorf("read timeout after %v", timeout)
	}
}

// extractUsage parses usage data from a turn/completed notification.
func extractUsage(msg *Message) agent.UsageReport {
	if len(msg.Params) == 0 {
		return agent.UsageReport{}
	}
	var params struct {
		Usage struct {
			InputTokens  int64 `json:"inputTokens"`
			OutputTokens int64 `json:"outputTokens"`
			TotalTokens  int64 `json:"totalTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return agent.UsageReport{}
	}
	return agent.UsageReport{
		InputTokens:  params.Usage.InputTokens,
		OutputTokens: params.Usage.OutputTokens,
		TotalTokens:  params.Usage.TotalTokens,
	}
}

// extractOutput extracts the output text from a turn/completed notification.
func extractOutput(msg *Message) string {
	if len(msg.Params) == 0 {
		return ""
	}
	var params struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return ""
	}
	return params.Output
}

// buildDynamicTools constructs the list of dynamic tools to provide to Codex
// based on the tracker type.
func buildDynamicTools(t tracker.Tracker) []DynamicTool {
	if t == nil {
		return nil
	}
	// For now, always add both tools. The actual execution will be a no-op
	// until we have access to the raw tracker client.
	return []DynamicTool{
		{
			Name:        "linear_graphql",
			Description: "Execute a GraphQL query against the Linear API",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "GraphQL query string"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "plane_rest",
			Description: "Make a REST API call to Plane",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method": map[string]any{"type": "string", "description": "HTTP method"},
					"path":   map[string]any{"type": "string", "description": "API path"},
					"body":   map[string]any{"type": "string", "description": "Request body as JSON string"},
				},
				"required": []string{"method", "path"},
			},
		},
	}
}
