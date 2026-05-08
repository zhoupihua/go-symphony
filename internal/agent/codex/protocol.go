package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// JSON-RPC 2.0 constants
const (
	JSONRPCVersion = "2.0"
)

// Method name constants
const (
	MethodInitialize               = "initialize"
	MethodInitialized              = "initialized"
	MethodThreadStart              = "thread/start"
	MethodTurnStart                = "turn/start"
	MethodTurnCompleted            = "turn/completed"
	MethodTurnFailed               = "turn/failed"
	MethodTurnCancelled            = "turn/cancelled"
	MethodItemCommandApproval      = "item/command_approval"
	MethodItemFileChangeApproval   = "item/file_change_approval"
	MethodExecCommandApproval      = "exec_command_approval"
	MethodApplyPatchApproval       = "apply_patch_approval"
	MethodItemToolCall             = "item/tool_call"
	MethodItemToolRequestUserInput = "item/tool_request_user_input"
)

// Message represents a JSON-RPC message. It can be a request, response, or notification.
//   - Request: has ID and Method
//   - Response: has ID but no Method (may have Result or Error)
//   - Notification: has Method but no ID
type Message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// IsRequest returns true if the message is a JSON-RPC request (has both ID and Method).
func (m *Message) IsRequest() bool {
	return m.ID != nil && m.Method != ""
}

// IsResponse returns true if the message is a JSON-RPC response (has ID but no Method).
func (m *Message) IsResponse() bool {
	return m.ID != nil && m.Method == ""
}

// IsNotification returns true if the message is a JSON-RPC notification (has Method but no ID).
func (m *Message) IsNotification() bool {
	return m.ID == nil && m.Method != ""
}

// RPCError represents a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Request creates a new JSON-RPC request message.
func Request(id int, method string, params any) (*Message, error) {
	rawID, err := json.Marshal(id)
	if err != nil {
		return nil, fmt.Errorf("marshal id: %w", err)
	}
	rawIDCopy := json.RawMessage(rawID)

	msg := &Message{
		JSONRPC: JSONRPCVersion,
		ID:      &rawIDCopy,
		Method:  method,
	}

	if params != nil {
		rawParams, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		msg.Params = json.RawMessage(rawParams)
	}

	return msg, nil
}

// Notification creates a new JSON-RPC notification message.
func Notification(method string, params any) (*Message, error) {
	msg := &Message{
		JSONRPC: JSONRPCVersion,
		Method:  method,
	}

	if params != nil {
		rawParams, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		msg.Params = json.RawMessage(rawParams)
	}

	return msg, nil
}

// Response creates a new JSON-RPC response message.
func Response(id int, result any) (*Message, error) {
	rawID, err := json.Marshal(id)
	if err != nil {
		return nil, fmt.Errorf("marshal id: %w", err)
	}
	rawIDCopy := json.RawMessage(rawID)

	msg := &Message{
		JSONRPC: JSONRPCVersion,
		ID:      &rawIDCopy,
	}

	if result != nil {
		rawResult, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		msg.Result = json.RawMessage(rawResult)
	}

	return msg, nil
}

// ErrorResponse creates a new JSON-RPC error response.
func ErrorResponse(id int, err *RPCError) *Message {
	rawID, marshalErr := json.Marshal(id)
	if marshalErr != nil {
		// Fallback to a zero ID if marshaling fails (should never happen for int)
		rawID = []byte("0")
	}
	rawIDCopy := json.RawMessage(rawID)

	return &Message{
		JSONRPC: JSONRPCVersion,
		ID:      &rawIDCopy,
		Error:   err,
	}
}

// Specific params types for Codex protocol:

// InitializeParams represents the parameters for the initialize request.
type InitializeParams struct {
	Capabilities map[string]any `json:"capabilities"`
	ClientInfo   ClientInfo     `json:"clientInfo"`
}

// ClientInfo describes the client connecting to the Codex agent.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializedParams represents the parameters for the initialized notification.
type InitializedParams struct{}

// ThreadStartParams represents the parameters for the thread/start notification.
type ThreadStartParams struct {
	ApprovalPolicy string        `json:"approvalPolicy"`
	Sandbox        string        `json:"sandbox,omitempty"`
	CWD            string        `json:"cwd"`
	DynamicTools   []DynamicTool `json:"dynamicTools,omitempty"`
}

// DynamicTool describes a tool that can be provided to the agent at thread start.
type DynamicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// TurnStartParams represents the parameters for the turn/start request.
type TurnStartParams struct {
	ThreadID       string `json:"threadId"`
	Input          string `json:"input"`
	CWD            string `json:"cwd,omitempty"`
	Title          string `json:"title,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	SandboxPolicy  string `json:"sandboxPolicy,omitempty"`
}

// ApprovalDecision represents a response to an approval request.
type ApprovalDecision struct {
	Decision string `json:"decision"` // "approve" or "deny"
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	Success      bool   `json:"success"`
	Output       string `json:"output,omitempty"`
	ContentItems []any  `json:"contentItems,omitempty"`
}

// Line-delimited JSON framing

// Encoder writes JSON-RPC messages as line-delimited JSON.
type Encoder struct {
	w io.Writer
}

// NewEncoder creates a new Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Encode writes a JSON-RPC message as a single line of JSON followed by a newline.
func (e *Encoder) Encode(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	if _, err := e.w.Write(data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if _, err := e.w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return nil
}

// Decoder reads JSON-RPC messages from line-delimited JSON.
type Decoder struct {
	scanner *bufio.Scanner
}

// NewDecoder creates a new Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max buffer
	return &Decoder{scanner: s}
}

// Decode reads and decodes the next JSON-RPC message from the stream.
// Returns io.EOF when there are no more messages.
func (d *Decoder) Decode() (*Message, error) {
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return nil, fmt.Errorf("read message: %w", err)
		}
		return nil, io.EOF
	}
	line := d.scanner.Bytes()
	if len(line) == 0 {
		return nil, fmt.Errorf("empty line")
	}
	var msg Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	return &msg, nil
}
