package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/zhoupihua/go-symphony/internal/agent"
	"github.com/zhoupihua/go-symphony/internal/sshclient"
)

// Compile-time interface checks.
var (
	_ agent.Agent   = (*ClaudeAdapter)(nil)
	_ agent.Session = (*ClaudeSession)(nil)
)

// ClaudeAdapter implements agent.Agent for Claude Code CLI.
type ClaudeAdapter struct {
	command        string
	permissionMode string
	allowedTools   []string
	maxTurns       int
}

// NewAdapter creates a ClaudeAdapter from adapter-specific config.
func NewAdapter(cfg map[string]any) (agent.Agent, error) {
	a := &ClaudeAdapter{
		command:  "claude",
		maxTurns: 10,
	}

	if v, ok := cfg["command"].(string); ok && v != "" {
		a.command = v
	}
	if v, ok := cfg["permission_mode"].(string); ok && v != "" {
		a.permissionMode = v
	}
	if v, ok := cfg["allowed_tools"].([]string); ok && len(v) > 0 {
		a.allowedTools = v
	}
	if v, ok := cfg["allowed_tools"].([]any); ok && len(v) > 0 {
		tools := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				tools = append(tools, s)
			}
		}
		a.allowedTools = tools
	}
	if v, ok := cfg["max_turns"].(float64); ok && v > 0 {
		a.maxTurns = int(v)
	}

	return a, nil
}

// Register registers the Claude adapter in the agent registry.
func Register() {
	agent.RegisterAgent("claude", NewAdapter)
}

// StartSession creates a new ClaudeSession. Unlike Codex, Claude Code doesn't
// maintain a persistent subprocess — each turn spawns a new process.
func (a *ClaudeAdapter) StartSession(_ context.Context, opts agent.SessionOptions) (agent.Session, error) {
	return &ClaudeSession{
		adapter: a,
		opts:    opts,
	}, nil
}

// ClaudeSession implements agent.Session for Claude Code.
type ClaudeSession struct {
	adapter   *ClaudeAdapter
	opts      agent.SessionOptions
	turnCount int
}

// RunTurn spawns a `claude -p <prompt> --output-format stream-json` subprocess,
// reads the NDJSON output, and maps events to a TurnResult.
// When WorkerHost is set, each turn is executed via SSH.
func (s *ClaudeSession) RunTurn(ctx context.Context, prompt string, opts agent.TurnOptions) (agent.TurnResult, error) {
	s.turnCount++

	if s.opts.WorkerHost != "" {
		return s.runTurnSSH(ctx, prompt, opts)
	}
	return s.runTurnLocal(ctx, prompt, opts)
}

// runTurnLocal executes a claude turn as a local subprocess.
func (s *ClaudeSession) runTurnLocal(ctx context.Context, prompt string, opts agent.TurnOptions) (agent.TurnResult, error) {
	cmd := s.buildCommand(prompt, opts)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude: get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude: start process: %w", err)
	}

	// Parse the NDJSON stream.
	result, parseErr := parseStream(ctx, stdout)

	// Wait for process to finish.
	waitErr := cmd.Wait()

	if parseErr != nil {
		return agent.TurnResult{}, fmt.Errorf("claude: parse stream: %w", parseErr)
	}

	if waitErr != nil {
		return agent.TurnResult{}, fmt.Errorf("claude: process exited with error: %w", waitErr)
	}

	return result, nil
}

// runTurnSSH executes a claude turn over SSH.
func (s *ClaudeSession) runTurnSSH(ctx context.Context, prompt string, opts agent.TurnOptions) (agent.TurnResult, error) {
	sshCfg := sshclient.Config{
		Host: s.opts.WorkerHost,
	}
	sshCl := sshclient.New(sshCfg)

	sshConn, err := sshCl.Dial(ctx)
	if err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: dial: %w", err)
	}
	defer sshCl.Close(sshConn)

	slog.Debug("claude ssh: connected for turn", "host", s.opts.WorkerHost)

	// Build the claude command args.
	args := s.buildSSHArgs(prompt, opts)
	remoteCmd := strings.Join(args, " ")
	fullCmd := sshclient.BuildCommand(remoteCmd, s.opts.WorkspacePath)

	session, err := sshConn.NewSession()
	if err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: new session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: stdout pipe: %w", err)
	}

	if err := session.Start(fullCmd); err != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: start: %w", err)
	}

	result, parseErr := parseStream(ctx, stdout)
	waitErr := session.Wait()

	if parseErr != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: parse stream: %w", parseErr)
	}
	if waitErr != nil {
		return agent.TurnResult{}, fmt.Errorf("claude ssh: process exited with error: %w", waitErr)
	}

	return result, nil
}

// buildSSHArgs constructs the claude command arguments for SSH execution.
func (s *ClaudeSession) buildSSHArgs(prompt string, opts agent.TurnOptions) []string {
	parts := strings.Fields(s.adapter.command)
	if len(parts) == 0 {
		parts = []string{"claude"}
	}

	args := make([]string, 0)
	args = append(args, parts...)

	args = append(args, "-p", shellQuoteArg(prompt), "--output-format", "stream-json")

	permMode := s.adapter.permissionMode
	if opts.ApprovalPolicy != "" {
		permMode = opts.ApprovalPolicy
	}
	switch permMode {
	case "auto", "bypassPermissions", "dangerously-skip-permissions":
		args = append(args, "--dangerously-skip-permissions")
	default:
		if len(s.adapter.allowedTools) > 0 {
			args = append(args, "--allowedTools", strings.Join(s.adapter.allowedTools, ","))
		}
	}

	maxTurns := s.adapter.maxTurns
	if opts.MaxTurns > 0 {
		maxTurns = opts.MaxTurns
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}

	return args
}

// shellQuoteArg wraps a prompt string in single quotes for shell safety.
func shellQuoteArg(s string) string {
	result := make([]byte, 0, len(s)+2)
	result = append(result, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			result = append(result, '\'', '\\', '\'', '\'')
		} else {
			result = append(result, s[i])
		}
	}
	result = append(result, '\'')
	return string(result)
}

// Close is a no-op for Claude Code (no persistent session).
func (s *ClaudeSession) Close() error {
	return nil
}

// buildCommand constructs the exec.Cmd for a claude turn.
func (s *ClaudeSession) buildCommand(prompt string, opts agent.TurnOptions) *exec.Cmd {
	// Parse the command string to allow custom claude paths/flags.
	parts := strings.Fields(s.adapter.command)
	if len(parts) == 0 {
		parts = []string{"claude"}
	}

	args := []string{}
	if len(parts) > 1 {
		args = append(args, parts[1:]...)
	}

	args = append(args, "-p", prompt, "--output-format", "stream-json")

	// Permission mode.
	permMode := s.adapter.permissionMode
	if opts.ApprovalPolicy != "" {
		permMode = opts.ApprovalPolicy
	}
	switch permMode {
	case "auto", "bypassPermissions", "dangerously-skip-permissions":
		args = append(args, "--dangerously-skip-permissions")
	default:
		// Use allowed tools if specified.
		if len(s.adapter.allowedTools) > 0 {
			args = append(args, "--allowedTools", strings.Join(s.adapter.allowedTools, ","))
		}
	}

	// Max turns.
	maxTurns := s.adapter.maxTurns
	if opts.MaxTurns > 0 {
		maxTurns = opts.MaxTurns
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}

	cmd := exec.Command(parts[0], args...)
	cmd.Dir = s.opts.WorkspacePath
	return cmd
}

// --- Stream parsing ---

// streamEvent represents a single NDJSON event from Claude Code.
type streamEvent struct {
	Type    string          `json:"type"`
	SubType string          `json:"subtype,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CostUSD float64         `json:"cost_usd,omitempty"`
	// Usage fields (Claude Code SDK format)
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
}

// parseStream reads NDJSON events from the reader and builds a TurnResult.
func parseStream(ctx context.Context, r io.Reader) (agent.TurnResult, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var result agent.TurnResult
	var outputParts []string
	var totalInput, totalOutput int64

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt streamEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			// Skip malformed lines.
			continue
		}

		switch evt.Type {
		case "assistant":
			// Assistant message — extract text content.
			text := extractText(evt.Content)
			if text != "" {
				outputParts = append(outputParts, text)
			}
		case "result":
			// Final result event.
			result.Completed = true
			text := extractText(evt.Content)
			if text != "" {
				outputParts = append(outputParts, text)
			}
			totalInput += evt.InputTokens
			totalOutput += evt.OutputTokens
		case "usage":
			totalInput += evt.InputTokens
			totalOutput += evt.OutputTokens
		}
	}

	result.Output = strings.Join(outputParts, "\n")
	result.Usage = agent.UsageReport{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	return result, nil
}

// extractText extracts text from a content field that may be a string or
// an array of content blocks.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try as array of content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}
