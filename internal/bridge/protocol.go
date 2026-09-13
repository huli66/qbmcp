package bridge

import (
	"crypto/rand"
	"encoding/hex"
)

const MaxMessage = 4 << 20

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type BridgeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *BridgeError) Error() string            { return e.Code + ": " + e.Message }
func failure(code, message string) *BridgeError { return &BridgeError{code, message} }

type ToolDef struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Kind         string         `json:"kind"`
}

type Message struct {
	Version       int            `json:"version"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	ConnectionID  string         `json:"connectionId,omitempty"`
	SchemaVersion uint64         `json:"schemaVersion,omitempty"`
	Tools         []ToolDef      `json:"tools,omitempty"`
	Name          string         `json:"name,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	Result        any            `json:"result,omitempty"`
	Error         *BridgeError   `json:"error,omitempty"`
}
