package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRequest(t *testing.T) {
	params := map[string]string{"key": "value"}
	msg, err := Request(1, MethodInitialize, params)
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	// Check JSON-RPC version
	if msg.JSONRPC != JSONRPCVersion {
		t.Errorf("JSONRPC = %q, want %q", msg.JSONRPC, JSONRPCVersion)
	}
	// Check ID is present
	if msg.ID == nil {
		t.Fatal("ID is nil, want non-nil")
	}
	var id int
	if err := json.Unmarshal(*msg.ID, &id); err != nil {
		t.Fatalf("unmarshal ID: %v", err)
	}
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}
	// Check method
	if msg.Method != MethodInitialize {
		t.Errorf("Method = %q, want %q", msg.Method, MethodInitialize)
	}
	// Check params
	if len(msg.Params) == 0 {
		t.Fatal("Params is empty")
	}
	var gotParams map[string]string
	if err := json.Unmarshal(msg.Params, &gotParams); err != nil {
		t.Fatalf("unmarshal Params: %v", err)
	}
	if gotParams["key"] != "value" {
		t.Errorf("Params[key] = %q, want %q", gotParams["key"], "value")
	}
	// Check no result or error
	if msg.Result != nil {
		t.Error("Result should be nil for request")
	}
	if msg.Error != nil {
		t.Error("Error should be nil for request")
	}
}

func TestRequestNilParams(t *testing.T) {
	msg, err := Request(42, MethodThreadStart, nil)
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if len(msg.Params) != 0 {
		t.Errorf("Params = %q, want empty", string(msg.Params))
	}
}

func TestNotification(t *testing.T) {
	params := InitializedParams{}
	msg, err := Notification(MethodInitialized, params)
	if err != nil {
		t.Fatalf("Notification() error = %v", err)
	}

	// Check no ID
	if msg.ID != nil {
		t.Error("ID should be nil for notification")
	}
	// Check method
	if msg.Method != MethodInitialized {
		t.Errorf("Method = %q, want %q", msg.Method, MethodInitialized)
	}
	// Check JSON-RPC version
	if msg.JSONRPC != JSONRPCVersion {
		t.Errorf("JSONRPC = %q, want %q", msg.JSONRPC, JSONRPCVersion)
	}
}

func TestNotificationNilParams(t *testing.T) {
	msg, err := Notification(MethodInitialized, nil)
	if err != nil {
		t.Fatalf("Notification() error = %v", err)
	}
	if len(msg.Params) != 0 {
		t.Errorf("Params = %q, want empty", string(msg.Params))
	}
}

func TestResponse(t *testing.T) {
	result := map[string]string{"status": "ok"}
	msg, err := Response(1, result)
	if err != nil {
		t.Fatalf("Response() error = %v", err)
	}

	// Check ID
	if msg.ID == nil {
		t.Fatal("ID is nil, want non-nil")
	}
	var id int
	if err := json.Unmarshal(*msg.ID, &id); err != nil {
		t.Fatalf("unmarshal ID: %v", err)
	}
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}
	// Check no method
	if msg.Method != "" {
		t.Errorf("Method = %q, want empty", msg.Method)
	}
	// Check result
	if len(msg.Result) == 0 {
		t.Fatal("Result is empty")
	}
	var gotResult map[string]string
	if err := json.Unmarshal(msg.Result, &gotResult); err != nil {
		t.Fatalf("unmarshal Result: %v", err)
	}
	if gotResult["status"] != "ok" {
		t.Errorf("Result[status] = %q, want %q", gotResult["status"], "ok")
	}
	// Check no error
	if msg.Error != nil {
		t.Error("Error should be nil for success response")
	}
}

func TestResponseNilResult(t *testing.T) {
	msg, err := Response(5, nil)
	if err != nil {
		t.Fatalf("Response() error = %v", err)
	}
	if len(msg.Result) != 0 {
		t.Errorf("Result = %q, want empty", string(msg.Result))
	}
}

func TestErrorResponse(t *testing.T) {
	rpcErr := &RPCError{
		Code:    -32600,
		Message: "Invalid Request",
	}
	msg := ErrorResponse(1, rpcErr)

	// Check ID
	if msg.ID == nil {
		t.Fatal("ID is nil, want non-nil")
	}
	var id int
	if err := json.Unmarshal(*msg.ID, &id); err != nil {
		t.Fatalf("unmarshal ID: %v", err)
	}
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}
	// Check error
	if msg.Error == nil {
		t.Fatal("Error is nil, want non-nil")
	}
	if msg.Error.Code != -32600 {
		t.Errorf("Error.Code = %d, want -32600", msg.Error.Code)
	}
	if msg.Error.Message != "Invalid Request" {
		t.Errorf("Error.Message = %q, want %q", msg.Error.Message, "Invalid Request")
	}
	// Check no method or result
	if msg.Method != "" {
		t.Errorf("Method = %q, want empty", msg.Method)
	}
	if msg.Result != nil {
		t.Error("Result should be nil for error response")
	}
}

func TestEncoderDecoderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	original, err := Request(1, MethodInitialize, InitializeParams{
		Capabilities: map[string]any{"foo": true},
		ClientInfo:   ClientInfo{Name: "test", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	if err := enc.Encode(original); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	dec := NewDecoder(&buf)
	decoded, err := dec.Decode()
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Compare key fields
	if decoded.JSONRPC != original.JSONRPC {
		t.Errorf("JSONRPC = %q, want %q", decoded.JSONRPC, original.JSONRPC)
	}
	if decoded.Method != original.Method {
		t.Errorf("Method = %q, want %q", decoded.Method, original.Method)
	}
	// Compare IDs
	var origID, decID int
	if err := json.Unmarshal(*original.ID, &origID); err != nil {
		t.Fatalf("unmarshal original ID: %v", err)
	}
	if err := json.Unmarshal(*decoded.ID, &decID); err != nil {
		t.Fatalf("unmarshal decoded ID: %v", err)
	}
	if decID != origID {
		t.Errorf("ID = %d, want %d", decID, origID)
	}
	// Compare params
	if !bytes.Equal(decoded.Params, original.Params) {
		t.Errorf("Params = %q, want %q", string(decoded.Params), string(original.Params))
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	input := strings.NewReader("{invalid json}\n")
	dec := NewDecoder(input)
	_, err := dec.Decode()
	if err == nil {
		t.Fatal("Decode() should return error for invalid JSON")
	}
}

func TestDecodeEmptyLine(t *testing.T) {
	input := strings.NewReader("\n")
	dec := NewDecoder(input)
	_, err := dec.Decode()
	if err == nil {
		t.Fatal("Decode() should return error for empty line")
	}
	if !strings.Contains(err.Error(), "empty line") {
		t.Errorf("error = %q, want containing 'empty line'", err.Error())
	}
}

func TestDecodeMultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	msgs := []*Message{}
	for i := range 3 {
		msg, err := Request(i, MethodTurnStart, TurnStartParams{
			ThreadID: "thread-1",
			Input:    "prompt",
		})
		if err != nil {
			t.Fatalf("Request(%d) error = %v", i, err)
		}
		msgs = append(msgs, msg)
		if err := enc.Encode(msg); err != nil {
			t.Fatalf("Encode(%d) error = %v", i, err)
		}
	}

	dec := NewDecoder(&buf)
	for i, expected := range msgs {
		decoded, err := dec.Decode()
		if err != nil {
			t.Fatalf("Decode(%d) error = %v", i, err)
		}
		var decID int
		if err := json.Unmarshal(*decoded.ID, &decID); err != nil {
			t.Fatalf("unmarshal decoded ID[%d]: %v", i, err)
		}
		if decID != i {
			t.Errorf("message %d: ID = %d, want %d", i, decID, i)
		}
		if decoded.Method != expected.Method {
			t.Errorf("message %d: Method = %q, want %q", i, decoded.Method, expected.Method)
		}
	}

	// No more messages
	_, err := dec.Decode()
	if !errors.Is(err, io.EOF) {
		t.Errorf("after all messages: error = %v, want io.EOF", err)
	}
}

func TestMessageTypeDetection(t *testing.T) {
	// Request: has ID and Method
	req, _ := Request(1, MethodInitialize, nil)
	if !req.IsRequest() {
		t.Error("request should be detected as IsRequest()")
	}
	if req.IsResponse() {
		t.Error("request should not be detected as IsResponse()")
	}
	if req.IsNotification() {
		t.Error("request should not be detected as IsNotification()")
	}

	// Response: has ID, no Method
	resp, _ := Response(1, map[string]string{"ok": "true"})
	if !resp.IsResponse() {
		t.Error("response should be detected as IsResponse()")
	}
	if resp.IsRequest() {
		t.Error("response should not be detected as IsRequest()")
	}
	if resp.IsNotification() {
		t.Error("response should not be detected as IsNotification()")
	}

	// Notification: has Method, no ID
	noti, _ := Notification(MethodInitialized, nil)
	if !noti.IsNotification() {
		t.Error("notification should be detected as IsNotification()")
	}
	if noti.IsRequest() {
		t.Error("notification should not be detected as IsRequest()")
	}
	if noti.IsResponse() {
		t.Error("notification should not be detected as IsResponse()")
	}
}

func TestInitializeParamsSerialization(t *testing.T) {
	params := InitializeParams{
		Capabilities: map[string]any{
			"streaming": true,
			"maxTurns":  10,
		},
		ClientInfo: ClientInfo{
			Name:    "symphony",
			Version: "0.1.0",
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got InitializeParams
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ClientInfo.Name != "symphony" {
		t.Errorf("ClientInfo.Name = %q, want %q", got.ClientInfo.Name, "symphony")
	}
	if got.ClientInfo.Version != "0.1.0" {
		t.Errorf("ClientInfo.Version = %q, want %q", got.ClientInfo.Version, "0.1.0")
	}
	streaming, ok := got.Capabilities["streaming"].(bool)
	if !ok || !streaming {
		t.Errorf("Capabilities[streaming] = %v, want true", got.Capabilities["streaming"])
	}

	// Also verify it works through a Request + round-trip
	msg, err := Request(1, MethodInitialize, params)
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	var decodedParams InitializeParams
	if err := json.Unmarshal(msg.Params, &decodedParams); err != nil {
		t.Fatalf("Unmarshal params: %v", err)
	}
	if decodedParams.ClientInfo.Name != "symphony" {
		t.Errorf("round-trip ClientInfo.Name = %q, want %q", decodedParams.ClientInfo.Name, "symphony")
	}
}

func TestThreadStartParamsSerialization(t *testing.T) {
	params := ThreadStartParams{
		ApprovalPolicy: "auto-approve",
		Sandbox:        "docker",
		CWD:            "/workspace/issue-1",
		DynamicTools: []DynamicTool{
			{
				Name:        "linear_comment",
				Description: "Post a comment on a Linear issue",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"body": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got ThreadStartParams
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ApprovalPolicy != "auto-approve" {
		t.Errorf("ApprovalPolicy = %q, want %q", got.ApprovalPolicy, "auto-approve")
	}
	if got.Sandbox != "docker" {
		t.Errorf("Sandbox = %q, want %q", got.Sandbox, "docker")
	}
	if got.CWD != "/workspace/issue-1" {
		t.Errorf("CWD = %q, want %q", got.CWD, "/workspace/issue-1")
	}
	if len(got.DynamicTools) != 1 {
		t.Fatalf("len(DynamicTools) = %d, want 1", len(got.DynamicTools))
	}
	if got.DynamicTools[0].Name != "linear_comment" {
		t.Errorf("DynamicTools[0].Name = %q, want %q", got.DynamicTools[0].Name, "linear_comment")
	}
	if got.DynamicTools[0].Description != "Post a comment on a Linear issue" {
		t.Errorf("DynamicTools[0].Description = %q, want expected description", got.DynamicTools[0].Description)
	}

	// Verify omit-empty for optional fields
	minimal := ThreadStartParams{
		ApprovalPolicy: "suggest",
		CWD:            "/workspace",
	}
	minData, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("Marshal minimal: %v", err)
	}
	if strings.Contains(string(minData), `"sandbox"`) {
		t.Error("minimal ThreadStartParams should not contain 'sandbox' field")
	}
	if strings.Contains(string(minData), `"dynamicTools"`) {
		t.Error("minimal ThreadStartParams should not contain 'dynamicTools' field")
	}
}

func TestTurnStartParamsSerialization(t *testing.T) {
	params := TurnStartParams{
		ThreadID:       "thread-abc-123",
		Input:          "Fix the authentication bug in login.go",
		CWD:            "/workspace/issue-42",
		Title:          "Fix auth bug",
		ApprovalPolicy: "auto-approve",
		SandboxPolicy:  "docker",
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got TurnStartParams
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ThreadID != "thread-abc-123" {
		t.Errorf("ThreadID = %q, want %q", got.ThreadID, "thread-abc-123")
	}
	if got.Input != "Fix the authentication bug in login.go" {
		t.Errorf("Input = %q, want expected input", got.Input)
	}
	if got.CWD != "/workspace/issue-42" {
		t.Errorf("CWD = %q, want %q", got.CWD, "/workspace/issue-42")
	}
	if got.Title != "Fix auth bug" {
		t.Errorf("Title = %q, want %q", got.Title, "Fix auth bug")
	}

	// Verify omit-empty for optional fields
	minimal := TurnStartParams{
		ThreadID: "thread-x",
		Input:    "hello",
	}
	minData, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("Marshal minimal: %v", err)
	}
	if strings.Contains(string(minData), `"cwd"`) {
		t.Error("minimal TurnStartParams should not contain 'cwd' field")
	}
	if strings.Contains(string(minData), `"title"`) {
		t.Error("minimal TurnStartParams should not contain 'title' field")
	}
	if strings.Contains(string(minData), `"approvalPolicy"`) {
		t.Error("minimal TurnStartParams should not contain 'approvalPolicy' field")
	}
	if strings.Contains(string(minData), `"sandboxPolicy"`) {
		t.Error("minimal TurnStartParams should not contain 'sandboxPolicy' field")
	}
}

func TestRPCErrorWithData(t *testing.T) {
	errData := json.RawMessage(`{"details":"field X is required"}`)
	rpcErr := &RPCError{
		Code:    -32602,
		Message: "Invalid params",
		Data:    errData,
	}
	msg := ErrorResponse(2, rpcErr)

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.Error == nil {
		t.Fatal("Error is nil after round-trip")
	}
	if got.Error.Code != -32602 {
		t.Errorf("Error.Code = %d, want -32602", got.Error.Code)
	}
	if got.Error.Message != "Invalid params" {
		t.Errorf("Error.Message = %q, want %q", got.Error.Message, "Invalid params")
	}
	if string(got.Error.Data) != `{"details":"field X is required"}` {
		t.Errorf("Error.Data = %q, want expected data", string(got.Error.Data))
	}
}

func TestApprovalDecisionSerialization(t *testing.T) {
	approve := ApprovalDecision{Decision: "approve"}
	data, err := json.Marshal(approve)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"decision":"approve"`) {
		t.Errorf("Marshal = %q, want containing decision:approve", string(data))
	}
}

func TestToolResultSerialization(t *testing.T) {
	result := ToolResult{
		Success: true,
		Output:  "file written successfully",
		ContentItems: []any{
			map[string]any{"type": "text", "text": "content"},
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got ToolResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if !got.Success {
		t.Error("Success = false, want true")
	}
	if got.Output != "file written successfully" {
		t.Errorf("Output = %q, want %q", got.Output, "file written successfully")
	}
	if len(got.ContentItems) != 1 {
		t.Errorf("len(ContentItems) = %d, want 1", len(got.ContentItems))
	}
}

func TestConstants(t *testing.T) {
	methods := []struct {
		name string
		val  string
	}{
		{"MethodInitialize", MethodInitialize},
		{"MethodInitialized", MethodInitialized},
		{"MethodThreadStart", MethodThreadStart},
		{"MethodTurnStart", MethodTurnStart},
		{"MethodTurnCompleted", MethodTurnCompleted},
		{"MethodTurnFailed", MethodTurnFailed},
		{"MethodTurnCancelled", MethodTurnCancelled},
		{"MethodItemCommandApproval", MethodItemCommandApproval},
		{"MethodItemFileChangeApproval", MethodItemFileChangeApproval},
		{"MethodExecCommandApproval", MethodExecCommandApproval},
		{"MethodApplyPatchApproval", MethodApplyPatchApproval},
		{"MethodItemToolCall", MethodItemToolCall},
		{"MethodItemToolRequestUserInput", MethodItemToolRequestUserInput},
	}
	for _, m := range methods {
		if m.val == "" {
			t.Errorf("%s is empty", m.name)
		}
	}
	if JSONRPCVersion != "2.0" {
		t.Errorf("JSONRPCVersion = %q, want %q", JSONRPCVersion, "2.0")
	}
}
