package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed web/demo.html
var demoHTML string

type App struct {
	bridge     *Bridge
	mcp        *mcp.Server
	config     Config
	started    time.Time
	registryMu sync.RWMutex
	dynamic    []string
	handler    http.Handler
}

func newApp(c Config, logger *slog.Logger) *App {
	a := &App{bridge: newBridge(), config: c, started: time.Now()}
	a.mcp = mcp.NewServer(&mcp.Implementation{Name: "qbmcp", Version: version}, &mcp.ServerOptions{
		Logger: logger, PageSize: 300,
		Instructions: "Connect a webpage first. Use qbmcp_discover_tools to refresh schemas, then invoke a dynamic tool or qbmcp_call_tool. Operations are never automatically retried.",
	})
	a.mcp.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				a.registryMu.RLock()
				defer a.registryMu.RUnlock()
			}
			return next(ctx, method, req)
		}
	})
	a.mcp.AddTool(&mcp.Tool{Name: "qbmcp_discover_tools", Description: "请求当前网页重新注册并返回所有工具 schema", InputSchema: map[string]any{"type": "object", "additionalProperties": false}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := decodeArguments(req.Params.Arguments)
		if err != nil {
			return toolResult(nil, err), nil
		}
		if len(args) != 0 {
			return toolResult(nil, failure("INVALID_ARGUMENTS", "发现工具不接受参数")), nil
		}
		v, err := a.bridge.invoke(ctx, "", nil, true)
		return toolResult(v, err), nil
	})
	a.mcp.AddTool(&mcp.Tool{Name: "qbmcp_call_tool", Description: "通过工具名和参数调用当前网页工具；先 discover 获取 schema", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}}, "required": []string{"name", "arguments"}, "additionalProperties": false,
	}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := decodeArguments(req.Params.Arguments)
		if err != nil {
			return toolResult(nil, err), nil
		}
		name, ok := args["name"].(string)
		arguments, argsOK := args["arguments"].(map[string]any)
		if !ok || !argsOK || len(args) != 2 {
			return toolResult(nil, failure("INVALID_ARGUMENTS", "需要 name 字符串和 arguments 对象")), nil
		}
		v, err := a.bridge.invoke(ctx, name, arguments, false)
		return toolResult(v, err), nil
	})
	a.bridge.onChange = a.publish
	mux := http.NewServeMux()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.mcp }, &mcp.StreamableHTTPOptions{SessionTimeout: 10 * time.Minute})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxMessage)
		mcpHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.health()) })
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) { a.bridge.serveWS(w, r, c.Token, a.originOK) })
	mux.HandleFunc("GET /demo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		fmt.Fprint(w, demoHTML)
	})
	mux.HandleFunc("GET /demo/api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"items": []string{"Go", "MCP", "WebSocket"}, "source": "qbmcp demo"})
	})
	a.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != fmt.Sprintf("127.0.0.1:%d", c.Port) && r.Host != fmt.Sprintf("localhost:%d", c.Port) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !a.originOK(o) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return a
}
func (a *App) originOK(origin string) bool {
	if origin == fmt.Sprintf("http://127.0.0.1:%d", a.config.Port) || origin == fmt.Sprintf("http://localhost:%d", a.config.Port) {
		return true
	}
	for _, o := range a.config.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}
func (a *App) publish(defs []ToolDef) {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	a.mcp.RemoveTools(a.dynamic...)
	a.dynamic = nil
	for _, d := range defs {
		t := &mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
		if d.OutputSchema != nil {
			t.OutputSchema = d.OutputSchema
		}
		a.mcp.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, err := decodeArguments(req.Params.Arguments)
			if err != nil {
				return toolResult(nil, err), nil
			}
			v, err := a.bridge.invoke(ctx, req.Params.Name, args, false)
			return toolResult(v, err), nil
		})
		a.dynamic = append(a.dynamic, d.Name)
	}
}
func toolResult(value any, err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	if err != nil {
		var be *BridgeError
		if !errors.As(err, &be) {
			be = failure("INTERNAL_ERROR", "调用失败")
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

type Health struct {
	Service         string `json:"service"`
	Version         string `json:"version"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	Address         string `json:"address"`
	PageConnected   bool   `json:"page_connected"`
	Ready           bool   `json:"ready"`
	ToolCount       int    `json:"tool_count"`
	MCPSessions     int    `json:"mcp_sessions"`
	PendingRequests int    `json:"pending_requests"`
}

func (a *App) health() Health {
	a.bridge.mu.Lock()
	h := Health{Service: "running", Version: version, UptimeSeconds: int64(time.Since(a.started).Seconds()), Address: fmt.Sprintf("127.0.0.1:%d", a.config.Port), PageConnected: a.bridge.active != nil, Ready: a.bridge.ready, ToolCount: len(a.bridge.tools), PendingRequests: len(a.bridge.pending) + len(a.bridge.queue)}
	a.bridge.mu.Unlock()
	for range a.mcp.Sessions() {
		h.MCPSessions++
	}
	return h
}
func (a *App) Close() {
	a.bridge.Close()
	for session := range a.mcp.Sessions() {
		_ = session.Close()
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
