package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qbmcp/internal/bridge"
	"qbmcp/internal/config"
)

type App struct {
	bridge     *bridge.Bridge
	mcp        *mcp.Server
	config     config.Config
	started    time.Time
	registryMu sync.RWMutex
	dynamic    []string
	handler    http.Handler
}

func New(c config.Config, logger *slog.Logger) *App {
	a := &App{config: c, started: time.Now()}
	a.bridge = bridge.New(bridge.Options{OnChange: a.publish})
	a.mcp = mcp.NewServer(&mcp.Implementation{Name: "qbmcp", Version: config.Version}, &mcp.ServerOptions{
		Logger: logger, PageSize: 300,
		Capabilities: &mcp.ServerCapabilities{Logging: &mcp.LoggingCapabilities{}, Tools: &mcp.ToolCapabilities{ListChanged: true}},
		Instructions: "All tools are provided by the connected webpage. The tool list is empty until a webpage registers its tools. Refresh tools/list when notified of changes. Operations are never automatically retried.",
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
	mux := http.NewServeMux()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return a.mcp }, &mcp.StreamableHTTPOptions{SessionTimeout: 10 * time.Minute})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, bridge.MaxMessage)
		mcpHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.Health()) })
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) { a.bridge.ServeWS(w, r, a.originOK) })
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

func (a *App) publish(defs []bridge.ToolDef) {
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
			v, err := a.bridge.Invoke(ctx, req.Params.Name, args)
			return toolResult(v, err), nil
		})
		a.dynamic = append(a.dynamic, d.Name)
	}
}

// ServeHTTP exposes MCP, WebSocket, and health through one handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }
