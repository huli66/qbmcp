package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testApp(t *testing.T) (*App, string) {
	t.Helper()
	s := httptest.NewUnstartedServer(nil)
	port := s.Listener.Addr().(*net.TCPAddr).Port
	a := newApp(Config{Port: port, Token: strings.Repeat("ab", 32)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Config.Handler = a.handler
	s.Start()
	t.Cleanup(func() { a.Close(); s.Close() })
	return a, s.URL
}
func testDef(name string) ToolDef {
	return ToolDef{Name: name, Description: "test", Kind: "api", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}, OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
}
func connectPage(t *testing.T, a *App, url string) (*websocket.Conn, string) {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(strings.Replace(url, "http", "ws", 1)+"/ws", http.Header{"Origin": []string{url}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err = c.WriteJSON(Message{Version: 1, Type: "hello", Token: a.config.Token}); err != nil {
		t.Fatal(err)
	}
	m := readMessage(t, c)
	if m.Type != "hello_ack" || m.ConnectionID == "" {
		t.Fatalf("hello: %+v", m)
	}
	return c, m.ConnectionID
}
func readMessage(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m Message
	if err := c.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}
func registerPage(t *testing.T, c *websocket.Conn, id string, defs ...ToolDef) Message {
	t.Helper()
	if err := c.WriteJSON(Message{Version: 1, Type: "register_tools", ConnectionID: id, Tools: defs}); err != nil {
		t.Fatal(err)
	}
	return readMessage(t, c)
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
func connectClient(t *testing.T, a *App, url string, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, options)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{a.config.Token}}}, nil)
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
func TestMCPAndWebSocketEndToEnd(t *testing.T) {
	a, url := testApp(t)
	notifications := make(chan struct{}, 16)
	c1 := connectClient(t, a, url, &mcp.ClientOptions{ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
		select {
		case notifications <- struct{}{}:
		default:
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	list, err := c1.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 2 {
		t.Fatalf("initial tools: %v %v", list, err)
	}
	r, err := c1.CallTool(ctx, &mcp.CallToolParams{Name: "qbmcp_discover_tools", Arguments: map[string]any{}})
	if err != nil || !r.IsError {
		t.Fatalf("missing page: %v %v", r, err)
	}
	p, id := connectPage(t, a, url)
	if m := registerPage(t, p, id, testDef("echo")); m.Type != "register_ack" {
		t.Fatalf("register: %+v", m)
	}
	select {
	case <-notifications:
	case <-time.After(3 * time.Second):
		t.Fatal("no tools changed notification")
	}
	list, err = c1.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 3 {
		t.Fatalf("dynamic tools: %v %v", list, err)
	}
	c2 := connectClient(t, a, url, nil)
	// The mock page echoes calls and responds to explicit discovery.
	pageDone := make(chan struct{})
	go func() {
		defer close(pageDone)
		for {
			var m Message
			if p.ReadJSON(&m) != nil {
				return
			}
			switch m.Type {
			case "call_tool":
				_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: m.ID, ConnectionID: id, Result: m.Arguments})
			case "request_tools":
				_ = p.WriteJSON(Message{Version: 1, Type: "register_tools", ID: m.ID, ConnectionID: id, Tools: []ToolDef{testDef("echo")}})
			}
		}
	}()
	var wg sync.WaitGroup
	for i, client := range []*mcp.ClientSession{c1, c2} {
		wg.Add(1)
		go func(i int, client *mcp.ClientSession) {
			defer wg.Done()
			name := "echo"
			args := map[string]any{"text": fmt.Sprint(i)}
			if i == 1 {
				name = "qbmcp_call_tool"
				args = map[string]any{"name": "echo", "arguments": args}
			}
			result, e := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if e != nil {
				t.Error(e)
				return
			}
			if result.IsError || result.Content[0].(*mcp.TextContent).Text != fmt.Sprintf(`{"text":"%d"}`, i) {
				t.Errorf("crossed result: %+v", result)
			}
		}(i, client)
	}
	wg.Wait()
	r, err = c1.CallTool(ctx, &mcp.CallToolParams{Name: "qbmcp_discover_tools", Arguments: map[string]any{}})
	if err != nil || r.IsError {
		t.Fatalf("discover: %v %v", r, err)
	}
	r, err = c1.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": 42}})
	if err != nil || !r.IsError {
		t.Fatalf("invalid arguments: %v %v", r, err)
	}
	if a.health().MCPSessions != 2 {
		t.Fatal("expected two MCP sessions")
	}
	_ = c1.Close()
	_ = c2.Close()
	if !a.health().PageConnected {
		t.Fatal("closing agents disconnected page")
	}
	p.Close()
	<-pageDone
	eventually(t, func() bool { return !a.health().PageConnected && a.health().ToolCount == 0 })
}
func TestReplacementAndAuthentication(t *testing.T) {
	a, url := testApp(t)
	p, id := connectPage(t, a, url)
	registerPage(t, p, id, testDef("echo"))
	bad, _, err := websocket.DefaultDialer.Dial(strings.Replace(url, "http", "ws", 1)+"/ws", http.Header{"Origin": []string{url}})
	if err != nil {
		t.Fatal(err)
	}
	_ = bad.WriteJSON(Message{Version: 1, Type: "hello", Token: "wrong"})
	var m Message
	if err = bad.ReadJSON(&m); !websocket.IsCloseError(err, 4003) {
		t.Fatalf("auth: %v", err)
	}
	bad.Close()
	if a.health().ToolCount != 1 {
		t.Fatal("unauthenticated connection replaced page")
	}
	result := make(chan error, 1)
	go func() {
		_, err := a.bridge.invoke(context.Background(), "echo", map[string]any{"text": "old"}, false)
		result <- err
	}()
	call := readMessage(t, p)
	if call.Type != "call_tool" {
		t.Fatal(call)
	}
	newPage, newID := connectPage(t, a, url)
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "PAGE_REPLACED") {
		t.Fatalf("replacement: %v", err)
	}
	if err = p.ReadJSON(&m); !websocket.IsCloseError(err, 4001) {
		t.Fatalf("replacement close: %v", err)
	}
	if a.health().ToolCount != 0 {
		t.Fatal("old tools retained")
	}
	registerPage(t, newPage, newID, testDef("new_echo"))
	if a.health().ToolCount != 1 {
		t.Fatal("new registry missing")
	}
}
func TestInvalidSchemaIsAtomic(t *testing.T) {
	a, url := testApp(t)
	p, id := connectPage(t, a, url)
	good := testDef("echo")
	registerPage(t, p, id, good)
	bad := testDef("bad")
	bad.InputSchema = map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "not-a-type"}}}
	for _, defs := range [][]ToolDef{{good, good}, {bad}, {testDef("qbmcp_reserved")}} {
		if m := registerPage(t, p, id, defs...); m.Type != "error" {
			t.Fatalf("accepted bad schema: %+v", m)
		}
		if a.health().ToolCount != 1 || !a.health().Ready {
			t.Fatal("invalid registration changed active tools")
		}
	}
}
func TestTimeoutCancelAndLateResult(t *testing.T) {
	a, url := testApp(t)
	a.bridge.timeout = 150 * time.Millisecond
	p, id := connectPage(t, a, url)
	registerPage(t, p, id, testDef("echo"))
	result := make(chan error, 1)
	go func() {
		_, err := a.bridge.invoke(context.Background(), "echo", map[string]any{"text": "timeout"}, false)
		result <- err
	}()
	call := readMessage(t, p)
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "TIMEOUT") {
		t.Fatalf("timeout: %v", err)
	}
	if m := readMessage(t, p); m.Type != "cancel" || m.ID != call.ID {
		t.Fatalf("cancel: %+v", m)
	}
	_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: call.ID, ConnectionID: id, Result: map[string]any{"text": "late"}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := a.bridge.invoke(ctx, "echo", map[string]any{"text": "cancel"}, false); result <- err }()
	second := readMessage(t, p)
	if second.ID == call.ID {
		t.Fatal("request ID reused")
	}
	cancel()
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "CANCELED") {
		t.Fatalf("cancel result: %v", err)
	}
	eventually(t, func() bool { return a.health().PendingRequests == 0 })
}
func TestHTTPGuardsAndHealth(t *testing.T) {
	a, url := testApp(t)
	for _, tc := range []struct {
		path, host, origin string
		status             int
	}{{"/health", "", "", 200}, {"/mcp", "", "", 401}, {"/health", "evil.example", "", 403}, {"/health", "", "https://evil.example", 403}, {"/demo", "", "", 200}} {
		req, _ := http.NewRequest("GET", url+tc.path, nil)
		if tc.host != "" {
			req.Host = tc.host
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%+v: %d", tc, resp.StatusCode)
		}
		if strings.Contains(string(body), a.config.Token) {
			t.Fatal("token leaked")
		}
		if tc.path == "/health" && tc.status == 200 {
			var h Health
			if json.Unmarshal(body, &h) != nil || h.Ready || h.PageConnected {
				t.Fatal("invalid disconnected health")
			}
		}
	}
	_, resp, err := websocket.DefaultDialer.Dial(strings.Replace(url, "http", "ws", 1)+"/ws", http.Header{"Origin": []string{"http://evil.example"}})
	if err == nil || resp.StatusCode != 403 {
		t.Fatal("bad origin accepted")
	}
}
func TestSchemaExternalReferencesRejected(t *testing.T) {
	d := testDef("external")
	d.InputSchema["properties"] = map[string]any{"x": map[string]any{"$ref": "file:///C:/secret.json"}}
	if _, err := compileTools([]ToolDef{d}); err == nil {
		t.Fatal("external reference accepted")
	}
}
func TestConfigValidation(t *testing.T) {
	c := Config{Port: 32300, Token: strings.Repeat("ab", 32), AllowedOrigins: []string{"http://localhost:3000"}}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"*", "null", "https://example.com", "http://localhost:3000/path", "http://user@localhost:3000"} {
		c.AllowedOrigins = []string{origin}
		if c.validate() == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
}

func TestQueueCapacityAndSchemaChange(t *testing.T) {
	a, url := testApp(t)
	p, id := connectPage(t, a, url)
	registerPage(t, p, id, testDef("echo"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 101)
	invoke := func() {
		_, err := a.bridge.invoke(ctx, "echo", map[string]any{"text": "queued"}, false)
		results <- err
	}
	go invoke()
	first := readMessage(t, p)
	for i := 0; i < 100; i++ {
		go invoke()
	}
	eventually(t, func() bool { return len(a.bridge.queue) == 100 })
	if _, err := a.bridge.invoke(ctx, "echo", map[string]any{"text": "overflow"}, false); err == nil || !strings.HasPrefix(err.Error(), "QUEUE_FULL") {
		t.Fatalf("queue limit: %v", err)
	}
	// Updating schemas invalidates queued calls, but the running call retains its validator.
	registerPage(t, p, id, testDef("echo"))
	_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: first.ID, ConnectionID: id, Result: map[string]any{"text": "first"}})
	success, changed := 0, 0
	for i := 0; i < 101; i++ {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else if strings.HasPrefix(err.Error(), "SCHEMA_CHANGED") {
				changed++
			} else {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queue did not drain")
		}
	}
	if success != 1 || changed != 100 {
		t.Fatalf("success=%d changed=%d", success, changed)
	}
}

func TestInvalidResultAndMessageLimit(t *testing.T) {
	a, url := testApp(t)
	p, id := connectPage(t, a, url)
	registerPage(t, p, id, testDef("echo"))
	result := make(chan error, 1)
	go func() {
		_, err := a.bridge.invoke(context.Background(), "echo", map[string]any{"text": "test"}, false)
		result <- err
	}()
	m := readMessage(t, p)
	_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: m.ID, ConnectionID: id, Result: map[string]any{"text": 42}})
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "INVALID_RESULT") {
		t.Fatalf("invalid result: %v", err)
	}
	_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: "oversized", ConnectionID: id, Result: strings.Repeat("x", maxMessage)})
	p.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := p.ReadJSON(&m); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversized close: %v", err)
	}
	eventually(t, func() bool { return !a.health().PageConnected })
}

func TestCanceledQueuedCallIsNeverSent(t *testing.T) {
	a, url := testApp(t)
	p, id := connectPage(t, a, url)
	registerPage(t, p, id, testDef("echo"))
	firstDone := make(chan error, 1)
	go func() {
		_, err := a.bridge.invoke(context.Background(), "echo", map[string]any{"text": "first"}, false)
		firstDone <- err
	}()
	first := readMessage(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := a.bridge.invoke(ctx, "echo", map[string]any{"text": "second"}, false)
		secondDone <- err
	}()
	eventually(t, func() bool { return len(a.bridge.queue) == 1 })
	cancel()
	if err := <-secondDone; err == nil || !strings.HasPrefix(err.Error(), "CANCELED") {
		t.Fatal(err)
	}
	_ = p.WriteJSON(Message{Version: 1, Type: "tool_result", ID: first.ID, ConnectionID: id, Result: map[string]any{"text": "first"}})
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return a.health().PendingRequests == 0 })
	p.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var m Message
	if err := p.ReadJSON(&m); err == nil {
		t.Fatalf("canceled job sent to page: %+v", m)
	}
}
