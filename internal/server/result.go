package server

import (
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qbmcp/internal/bridge"
)

func toolResult(value any, err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	if err != nil {
		var be *bridge.BridgeError
		if !errors.As(err, &be) {
			be = &bridge.BridgeError{Code: "INTERNAL_ERROR", Message: "调用失败"}
		}
		value = map[string]any{"error": be}
		r.IsError = true
	}
	b, _ := json.Marshal(value)
	r.Content = []mcp.Content{&mcp.TextContent{Text: string(b)}}
	if object, ok := value.(map[string]any); ok {
		r.StructuredContent = object
	}
	return r
}

// Normalize through JSON so schema validation sees only JSON values.
func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, &bridge.BridgeError{Code: "INVALID_ARGUMENTS", Message: "参数必须是 JSON object"}
	}
	return args, nil
}
