package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ainative/go-symphony/internal/tracker"
)

// handleToolCall processes a tool call request from the Codex server,
// executes the requested dynamic tool, and sends back the result.
func handleToolCall(ctx context.Context, msg *Message, enc *Encoder, t tracker.Tracker) error {
	var id int
	if msg.ID != nil {
		if err := json.Unmarshal(*msg.ID, &id); err != nil {
			return fmt.Errorf("codex: parse tool call ID: %w", err)
		}
	}

	// Extract tool name and input from params.
	var params struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return fmt.Errorf("codex: parse tool call params: %w", err)
		}
	}

	result, err := executeDynamicTool(ctx, params.Name, params.Input, t)
	if err != nil {
		// Send an error response.
		errResp := ErrorResponse(id, &RPCError{
			Code:    -32000,
			Message: fmt.Sprintf("tool execution error: %v", err),
		})
		if encodeErr := enc.Encode(errResp); encodeErr != nil {
			return fmt.Errorf("codex: send tool error response: %w", encodeErr)
		}
		return nil
	}

	resp, err := Response(id, result)
	if err != nil {
		return fmt.Errorf("codex: build tool result response: %w", err)
	}
	if err := enc.Encode(resp); err != nil {
		return fmt.Errorf("codex: send tool result response: %w", err)
	}

	return nil
}

// executeDynamicTool runs a dynamic tool and returns its result.
//
// Known limitation: The Tracker interface does not expose raw API clients,
// so actual tool execution returns "tool not available". The real implementation
// will need a way to pass the underlying client (e.g., Linear GraphQL client,
// Plane REST client).
func executeDynamicTool(_ context.Context, toolName string, _ json.RawMessage, _ tracker.Tracker) (*ToolResult, error) {
	switch toolName {
	case "linear_graphql":
		return &ToolResult{
			Success: false,
			Output:  "tool not available: linear_graphql requires raw client access",
		}, nil
	case "plane_rest":
		return &ToolResult{
			Success: false,
			Output:  "tool not available: plane_rest requires raw client access",
		}, nil
	default:
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("unknown tool: %s", toolName),
		}, nil
	}
}
