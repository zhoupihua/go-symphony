package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zhoupihua/go-symphony/internal/agent"
)

func TestNewAdapterDefaults(t *testing.T) {
	a, err := NewAdapter(map[string]any{})
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	ca := a.(*ClaudeAdapter)
	if ca.command != "claude" {
		t.Errorf("command = %q, want %q", ca.command, "claude")
	}
	if ca.maxTurns != 10 {
		t.Errorf("maxTurns = %d, want 10", ca.maxTurns)
	}
}

func TestNewAdapterCustom(t *testing.T) {
	a, err := NewAdapter(map[string]any{
		"command":         "claude --model sonnet",
		"permission_mode": "auto",
		"allowed_tools":   []any{"Read", "Write"},
		"max_turns":       float64(5),
	})
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	ca := a.(*ClaudeAdapter)
	if ca.command != "claude --model sonnet" {
		t.Errorf("command = %q, want %q", ca.command, "claude --model sonnet")
	}
	if ca.permissionMode != "auto" {
		t.Errorf("permissionMode = %q, want %q", ca.permissionMode, "auto")
	}
	if len(ca.allowedTools) != 2 {
		t.Errorf("len(allowedTools) = %d, want 2", len(ca.allowedTools))
	}
	if ca.maxTurns != 5 {
		t.Errorf("maxTurns = %d, want 5", ca.maxTurns)
	}
}

func TestStartSession(t *testing.T) {
	a, err := NewAdapter(map[string]any{})
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	sess, err := a.StartSession(context.Background(), agent.SessionOptions{WorkspacePath: "/tmp/test"})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil")
	}
}

func TestBuildCommandDefault(t *testing.T) {
	a, _ := NewAdapter(map[string]any{})
	sess, _ := a.StartSession(context.Background(), agent.SessionOptions{WorkspacePath: "/tmp/test"})

	cmd := sess.(*ClaudeSession).buildCommand("Fix the bug", agent.TurnOptions{})

	if cmd.Dir != "/tmp/test" {
		t.Errorf("Dir = %q, want /tmp/test", cmd.Dir)
	}
	args := cmd.Args
	// Check that key args are present.
	hasPrompt := false
	hasStreamJSON := false
	for _, arg := range args {
		if arg == "-p" {
			hasPrompt = true
		}
		if arg == "--output-format" {
			hasStreamJSON = true
		}
	}
	if !hasPrompt {
		t.Error("missing -p flag")
	}
	if !hasStreamJSON {
		t.Error("missing --output-format flag")
	}
}

func TestBuildCommandAutoPermission(t *testing.T) {
	a, _ := NewAdapter(map[string]any{"permission_mode": "auto"})
	sess, _ := a.StartSession(context.Background(), agent.SessionOptions{WorkspacePath: "/tmp/test"})

	cmd := sess.(*ClaudeSession).buildCommand("test", agent.TurnOptions{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "--dangerously-skip-permissions") {
		t.Error("auto permission mode should add --dangerously-skip-permissions")
	}
}

func TestBuildCommandAllowedTools(t *testing.T) {
	a, _ := NewAdapter(map[string]any{
		"allowed_tools": []any{"Read", "Write"},
	})
	sess, _ := a.StartSession(context.Background(), agent.SessionOptions{WorkspacePath: "/tmp/test"})

	cmd := sess.(*ClaudeSession).buildCommand("test", agent.TurnOptions{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "--allowedTools") {
		t.Error("should add --allowedTools flag")
	}
	if !strings.Contains(args, "Read,Write") {
		t.Error("should list allowed tools")
	}
}

func TestBuildCommandMaxTurns(t *testing.T) {
	a, _ := NewAdapter(map[string]any{"max_turns": float64(5)})
	sess, _ := a.StartSession(context.Background(), agent.SessionOptions{WorkspacePath: "/tmp/test"})

	cmd := sess.(*ClaudeSession).buildCommand("test", agent.TurnOptions{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "--max-turns 5") {
		t.Errorf("should add --max-turns 5, got: %s", args)
	}
}

func TestCloseIsNoOp(t *testing.T) {
	a, _ := NewAdapter(map[string]any{})
	sess, _ := a.StartSession(context.Background(), agent.SessionOptions{})

	if err := sess.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	var _ agent.Agent = (*ClaudeAdapter)(nil)
	var _ agent.Session = (*ClaudeSession)(nil)
}

// --- Stream parsing tests ---

// stringReader is a simple io.Reader from a string.
type stringReader struct {
	*strings.Reader
}

func TestParseStreamResult(t *testing.T) {
	lines := []string{
		`{"type":"assistant","content":"Working on it..."}`,
		`{"type":"result","content":"Fixed the bug"}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := parseStream(context.Background(), &stringReader{strings.NewReader(ndjson)})
	if err != nil {
		t.Fatalf("parseStream() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
	if !strings.Contains(result.Output, "Fixed the bug") {
		t.Errorf("Output = %q, want containing 'Fixed the bug'", result.Output)
	}
}

func TestParseStreamWithUsage(t *testing.T) {
	lines := []string{
		`{"type":"assistant","content":"Working..."}`,
		`{"type":"usage","input_tokens":100,"output_tokens":200}`,
		`{"type":"result","content":"Done","input_tokens":50,"output_tokens":75}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := parseStream(context.Background(), &stringReader{strings.NewReader(ndjson)})
	if err != nil {
		t.Fatalf("parseStream() error = %v", err)
	}
	if result.Usage.InputTokens != 150 {
		t.Errorf("InputTokens = %d, want 150", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 275 {
		t.Errorf("OutputTokens = %d, want 275", result.Usage.OutputTokens)
	}
}

func TestParseStreamEmptyOutput(t *testing.T) {
	lines := []string{
		`{"type":"result","content":""}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := parseStream(context.Background(), &stringReader{strings.NewReader(ndjson)})
	if err != nil {
		t.Fatalf("parseStream() error = %v", err)
	}
	if !result.Completed {
		t.Error("Completed = false, want true")
	}
}

func TestParseStreamContentBlocks(t *testing.T) {
	content := `[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]`
	lines := []string{
		`{"type":"assistant","content":` + content + `}`,
		`{"type":"result","content":"done"}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := parseStream(context.Background(), &stringReader{strings.NewReader(ndjson)})
	if err != nil {
		t.Fatalf("parseStream() error = %v", err)
	}
	if !strings.Contains(result.Output, "Hello") || !strings.Contains(result.Output, "World") {
		t.Errorf("Output = %q, want containing 'Hello' and 'World'", result.Output)
	}
}

func TestParseStreamMalformedJSON(t *testing.T) {
	lines := []string{
		`{invalid json}`,
		`{"type":"result","content":"ok"}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	result, err := parseStream(context.Background(), &stringReader{strings.NewReader(ndjson)})
	if err != nil {
		t.Fatalf("parseStream() error = %v", err)
	}
	if !result.Completed {
		t.Error("should still complete despite malformed line")
	}
}

func TestParseStreamContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	lines := []string{
		`{"type":"assistant","content":"starting"}`,
	}
	ndjson := strings.Join(lines, "\n") + "\n"

	// Use a reader that will block after the first line.
	_, err := parseStream(ctx, &stringReader{strings.NewReader(ndjson)})
	// May or may not error depending on timing, but should not hang.
	_ = err
}

func TestExtractTextString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	text := extractText(raw)
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
}

func TestExtractTextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]`)
	text := extractText(raw)
	if !strings.Contains(text, "line1") || !strings.Contains(text, "line2") {
		t.Errorf("text = %q, want containing line1 and line2", text)
	}
}

func TestExtractTextEmpty(t *testing.T) {
	text := extractText(nil)
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}
