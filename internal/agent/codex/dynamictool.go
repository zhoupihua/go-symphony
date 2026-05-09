package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zhoupihua/go-symphony/internal/tracker"
	"github.com/zhoupihua/go-symphony/internal/tracker/linear"
	"github.com/zhoupihua/go-symphony/internal/tracker/plane"
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

// executeDynamicTool runs a dynamic tool using the tracker's raw client.
func executeDynamicTool(ctx context.Context, toolName string, input json.RawMessage, t tracker.Tracker) (*ToolResult, error) {
	if t == nil {
		return &ToolResult{Success: false, Output: "no tracker configured"}, nil
	}

	provider, ok := t.(tracker.RawClientProvider)
	if !ok {
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("tracker does not expose raw client for tool: %s", toolName),
		}, nil
	}

	rawClient := provider.RawClient()

	switch toolName {
	case "linear_graphql":
		return executeLinearGraphQL(ctx, rawClient, input)
	case "plane_rest":
		return executePlaneREST(ctx, rawClient, input)
	default:
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("unknown tool: %s", toolName),
		}, nil
	}
}

// executeLinearGraphQL executes a GraphQL query against the Linear API.
func executeLinearGraphQL(ctx context.Context, rawClient any, input json.RawMessage) (*ToolResult, error) {
	client, ok := rawClient.(*linear.Client)
	if !ok {
		return &ToolResult{
			Success: false,
			Output:  "linear_graphql: tracker is not a Linear adapter",
		}, nil
	}

	var args struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("parse linear_graphql input: %w", err)
	}
	if args.Query == "" {
		return &ToolResult{Success: false, Output: "linear_graphql: query is required"}, nil
	}
	if countGraphQLOperations(args.Query) > 1 {
		return &ToolResult{Success: false, Output: "linear_graphql: only single-operation queries are allowed"}, nil
	}

	data, err := client.Query(ctx, args.Query, args.Variables)

	// Check for GraphQL errors — preserve response body per SPEC §10.5.
	var gqlErr *linear.GraphQLError
	if errors.As(err, &gqlErr) {
		// GraphQL errors present -> success=false but preserve the response body.
		body := data
		if body == nil {
			body = map[string]any{}
		}
		// Include errors in the output for debugging.
		body["errors"] = gqlErr.Errors
		resultJSON, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return &ToolResult{
				Success: false,
				Output:  fmt.Sprintf("linear_graphql: marshal error response: %v", marshalErr),
			}, nil
		}
		return &ToolResult{
			Success: false,
			Output:  string(resultJSON),
		}, nil
	}

	if err != nil {
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("linear_graphql error: %v", err),
		}, nil
	}

	resultJSON, err := json.Marshal(data)
	if err != nil {
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("linear_graphql: marshal result: %v", err),
		}, nil
	}

	return &ToolResult{
		Success: true,
		Output:  string(resultJSON),
	}, nil
}

// executePlaneREST executes a REST API call against the Plane API.
func executePlaneREST(ctx context.Context, rawClient any, input json.RawMessage) (*ToolResult, error) {
	client, ok := rawClient.(*plane.Client)
	if !ok {
		return &ToolResult{
			Success: false,
			Output:  "plane_rest: tracker is not a Plane adapter",
		}, nil
	}

	var args struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("parse plane_rest input: %w", err)
	}
	if args.Method == "" || args.Path == "" {
		return &ToolResult{Success: false, Output: "plane_rest: method and path are required"}, nil
	}

	var result map[string]any
	var err error

	switch args.Method {
	case "GET":
		result, err = client.Get(ctx, args.Path)
	case "POST":
		var body any
		if args.Body != "" {
			if jsonErr := json.Unmarshal([]byte(args.Body), &body); jsonErr != nil {
				return nil, fmt.Errorf("parse plane_rest body: %w", jsonErr)
			}
		}
		result, err = client.Post(ctx, args.Path, body)
	case "PATCH":
		var body any
		if args.Body != "" {
			if jsonErr := json.Unmarshal([]byte(args.Body), &body); jsonErr != nil {
				return nil, fmt.Errorf("parse plane_rest body: %w", jsonErr)
			}
		}
		result, err = client.Patch(ctx, args.Path, body)
	default:
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("plane_rest: unsupported method: %s", args.Method),
		}, nil
	}

	if err != nil {
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("plane_rest error: %v", err),
		}, nil
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return &ToolResult{
			Success: false,
			Output:  fmt.Sprintf("plane_rest: marshal result: %v", err),
		}, nil
	}

	return &ToolResult{
		Success: true,
		Output:  string(resultJSON),
	}, nil
}

// countGraphQLOperations counts the number of top-level operation definitions
// in a GraphQL query. It uses a simple heuristic: count occurrences of
// "query", "mutation", or "subscription" keywords that appear at the start
// of a line or after a closing brace, ignoring comments and strings.
func countGraphQLOperations(query string) int {
	count := 0
	inString := false
	escaped := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch {
		case escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			continue
		case ch == '#':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case i == 0 || query[i-1] == '{' || query[i-1] == '}' || query[i-1] == '\n':
			rest := query[i:]
			if strings.HasPrefix(rest, "query") && (len(rest) == 5 || !isAlphaNumByte(rest[5])) {
				count++
			} else if strings.HasPrefix(rest, "mutation") && (len(rest) == 8 || !isAlphaNumByte(rest[8])) {
				count++
			} else if strings.HasPrefix(rest, "subscription") && (len(rest) == 12 || !isAlphaNumByte(rest[12])) {
				count++
			}
		}
	}
	return count
}

func isAlphaNumByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
