package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qbmcp/internal/bridge"
	"qbmcp/internal/server"
)

func TestMCPAndWebSocketEndToEnd(t *testing.T) {
	a, url := testApp(t)
	notifications := make(chan struct{}, 16)
	c1 := connectClient(t, url, &mcp.ClientOptions{ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
		select {
		case notifications <- struct{}{}:
		default:
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	list, err := c1.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatalf("initial tools: %v %v", list, err)
	}
	if caps := c1.InitializeResult().Capabilities; caps.Tools == nil || !caps.Tools.ListChanged {
		t.Fatalf("missing dynamic tools capability: %+v", caps)
	}
	if _, err := c1.CallTool(ctx, &mcp.CallToolParams{Name: "echo"}); err == nil {
		t.Fatal("missing tool accepted")
	}
	p, id := connectPage(t, url)
	if m := registerPage(t, p, id, testDef("echo")); m.Type != "register_ack" {
		t.Fatalf("register: %+v", m)
	}
	select {
	case <-notifications:
	case <-time.After(3 * time.Second):
		t.Fatal("no tools changed notification")
	}
	list, err = c1.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "echo" {
		t.Fatalf("dynamic tools: %v %v", list, err)
	}
	c2 := connectClient(t, url, nil)
	// The mock page supplies the implementation for both MCP clients.
	pageDone := make(chan struct{})
	go func() {
		defer close(pageDone)
		for {
			var m bridge.Message
			if p.ReadJSON(&m) != nil {
				return
			}
			switch m.Type {
			case "call_tool":
				_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: m.ID, ConnectionID: id, Result: m.Arguments})
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
	r, err := c1.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": 42}})
	if err != nil || !r.IsError {
		t.Fatalf("invalid arguments: %v %v", r, err)
	}
	if a.Health().MCPSessions != 2 {
		t.Fatal("expected two MCP sessions")
	}
	_ = c2.Close()
	if !a.Health().PageConnected {
		t.Fatal("closing agents disconnected page")
	}
	p.Close()
	<-pageDone
	eventually(t, func() bool { return !a.Health().PageConnected && a.Health().ToolCount == 0 })
	select {
	case <-notifications:
	case <-time.After(3 * time.Second):
		t.Fatal("no disconnect tools changed notification")
	}
	list, err = c1.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatalf("disconnected tools: %v %v", list, err)
	}
}

func TestPageOwnsToolRegistry(t *testing.T) {
	a, url := testApp(t)
	notifications := make(chan struct{}, 16)
	client := connectClient(t, url, &mcp.ClientOptions{ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
		notifications <- struct{}{}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, id := connectPage(t, url)
	for _, name := range []string{"page_action", "qbmcp_page_action", ""} {
		var defs []bridge.ToolDef
		if name != "" {
			defs = []bridge.ToolDef{testDef(name)}
		}
		if m := registerPage(t, p, id, defs...); m.Type != "register_ack" {
			t.Fatalf("register: %+v", m)
		}
		select {
		case <-notifications:
		case <-time.After(3 * time.Second):
			t.Fatal("no registry update notification")
		}
		list, err := client.ListTools(ctx, nil)
		if err != nil || len(list.Tools) != len(defs) {
			t.Fatalf("page tool snapshot: %v %v", list, err)
		}
		if name != "" && list.Tools[0].Name != name {
			t.Fatalf("unexpected tool: %+v", list.Tools[0])
		}
		if h := a.Health(); !h.Ready || h.ToolCount != len(defs) {
			t.Fatalf("health disagrees with registry: %+v", h)
		}
	}

	if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "qbmcp_page_action", Arguments: map[string]any{"text": "removed"}}); err == nil {
		t.Fatal("MCP accepted a removed tool")
	}
}

func TestHTTPGuardsAndHealth(t *testing.T) {
	_, url := testApp(t)
	for _, tc := range []struct {
		path, host, origin string
		status             int
	}{{"/health", "", "", 200}, {"/health", "evil.example", "", 403}, {"/health", "", "https://evil.example", 403}, {"/demo", "", "", 404}, {"/demo/api/data", "", "", 404}} {
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
		if tc.path == "/health" && tc.status == 200 {
			var h server.Health
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
