package tests

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qbmcp/internal/bridge"
	"qbmcp/internal/config"
	"qbmcp/internal/server"
)

func testApp(t *testing.T) (*server.App, string) {
	t.Helper()
	s := httptest.NewUnstartedServer(nil)
	port := s.Listener.Addr().(*net.TCPAddr).Port
	a := server.New(config.Config{Port: port}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Config.Handler = a
	s.Start()
	t.Cleanup(func() { a.Close(); s.Close() })
	return a, s.URL
}

func testDef(name string) bridge.ToolDef {
	return bridge.ToolDef{Name: name, Description: "test", Kind: "api", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}, OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
}

func connectPage(t *testing.T, url string) (*websocket.Conn, string) {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(strings.Replace(url, "http", "ws", 1)+"/ws", http.Header{"Origin": []string{url}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err = c.WriteJSON(bridge.Message{Version: 1, Type: "hello"}); err != nil {
		t.Fatal(err)
	}
	m := readMessage(t, c)
	if m.Type != "hello_ack" || m.ConnectionID == "" {
		t.Fatalf("hello: %+v", m)
	}
	return c, m.ConnectionID
}

func readMessage(t *testing.T, c *websocket.Conn) bridge.Message {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m bridge.Message
	if err := c.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func registerPage(t *testing.T, c *websocket.Conn, id string, defs ...bridge.ToolDef) bridge.Message {
	t.Helper()
	if err := c.WriteJSON(bridge.Message{Version: 1, Type: "register_tools", ConnectionID: id, Tools: defs}); err != nil {
		t.Fatal(err)
	}
	return readMessage(t, c)
}

func connectClient(t *testing.T, url string, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, options)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func testBridge(t *testing.T, timeout time.Duration) (*bridge.Bridge, string) {
	t.Helper()
	b := bridge.New(bridge.Options{Timeout: timeout})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.ServeWS(w, r, func(origin string) bool { return origin == "http://"+r.Host })
	}))
	t.Cleanup(func() { b.Close(); s.Close() })
	return b, s.URL
}
