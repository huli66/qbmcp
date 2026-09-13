package tests

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"qbmcp/internal/bridge"
)

func TestReplacementAndHandshake(t *testing.T) {
	a, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	registerPage(t, p, id, testDef("echo"))
	bad, _, err := websocket.DefaultDialer.Dial(strings.Replace(url, "http", "ws", 1)+"/ws", http.Header{"Origin": []string{url}})
	if err != nil {
		t.Fatal(err)
	}
	_ = bad.WriteJSON(bridge.Message{Version: 2, Type: "hello"})
	var m bridge.Message
	if err = bad.ReadJSON(&m); !websocket.IsCloseError(err, websocket.CloseProtocolError) {
		t.Fatalf("invalid handshake: %v", err)
	}
	bad.Close()
	if a.Snapshot().ToolCount != 1 {
		t.Fatal("invalid handshake replaced page")
	}
	result := make(chan error, 1)
	go func() {
		_, err := a.Invoke(context.Background(), "echo", map[string]any{"text": "old"})
		result <- err
	}()
	call := readMessage(t, p)
	if call.Type != "call_tool" {
		t.Fatal(call)
	}
	newPage, newID := connectPage(t, url)
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "PAGE_REPLACED") {
		t.Fatalf("replacement: %v", err)
	}
	if err = p.ReadJSON(&m); !websocket.IsCloseError(err, 4001) {
		t.Fatalf("replacement close: %v", err)
	}
	if a.Snapshot().ToolCount != 0 {
		t.Fatal("old tools retained")
	}
	registerPage(t, newPage, newID, testDef("new_echo"))
	if a.Snapshot().ToolCount != 1 {
		t.Fatal("new registry missing")
	}
}

func TestInvalidSchemaIsAtomic(t *testing.T) {
	a, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	good := testDef("echo")
	registerPage(t, p, id, good)
	bad := testDef("bad")
	bad.InputSchema = map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "not-a-type"}}}
	for _, defs := range [][]bridge.ToolDef{{good, good}, {bad}, {testDef("invalid name")}} {
		if m := registerPage(t, p, id, defs...); m.Type != "error" {
			t.Fatalf("accepted bad schema: %+v", m)
		}
		if a.Snapshot().ToolCount != 1 || !a.Snapshot().Ready {
			t.Fatal("invalid registration changed active tools")
		}
	}
}

func TestTimeoutCancelAndLateResult(t *testing.T) {
	a, url := testBridge(t, 150*time.Millisecond)
	p, id := connectPage(t, url)
	registerPage(t, p, id, testDef("echo"))
	result := make(chan error, 1)
	go func() {
		_, err := a.Invoke(context.Background(), "echo", map[string]any{"text": "timeout"})
		result <- err
	}()
	call := readMessage(t, p)
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "TIMEOUT") {
		t.Fatalf("timeout: %v", err)
	}
	if m := readMessage(t, p); m.Type != "cancel" || m.ID != call.ID {
		t.Fatalf("cancel: %+v", m)
	}
	_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: call.ID, ConnectionID: id, Result: map[string]any{"text": "late"}})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := a.Invoke(ctx, "echo", map[string]any{"text": "cancel"}); result <- err }()
	second := readMessage(t, p)
	if second.ID == call.ID {
		t.Fatal("request ID reused")
	}
	cancel()
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "CANCELED") {
		t.Fatalf("cancel result: %v", err)
	}
	eventually(t, func() bool { return a.Snapshot().PendingRequests == 0 })
}

func TestSchemaExternalReferencesRejected(t *testing.T) {
	_, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	d := testDef("external")
	d.InputSchema["properties"] = map[string]any{"x": map[string]any{"$ref": "file:///C:/secret.json"}}
	if m := registerPage(t, p, id, d); m.Type != "error" || m.Error.Code != "INVALID_SCHEMA" {
		t.Fatalf("external reference accepted: %+v", m)
	}
}

func TestQueueCapacityAndSchemaChange(t *testing.T) {
	a, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	registerPage(t, p, id, testDef("echo"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 101)
	invoke := func() {
		_, err := a.Invoke(ctx, "echo", map[string]any{"text": "queued"})
		results <- err
	}
	go invoke()
	first := readMessage(t, p)
	for i := 0; i < 100; i++ {
		go invoke()
	}
	eventually(t, func() bool { return a.Snapshot().QueuedRequests == 100 })
	if _, err := a.Invoke(ctx, "echo", map[string]any{"text": "overflow"}); err == nil || !strings.HasPrefix(err.Error(), "QUEUE_FULL") {
		t.Fatalf("queue limit: %v", err)
	}
	// Updating schemas invalidates queued calls, but the running call retains its validator.
	registerPage(t, p, id, testDef("echo"))
	_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: first.ID, ConnectionID: id, Result: map[string]any{"text": "first"}})
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
	a, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	registerPage(t, p, id, testDef("echo"))
	result := make(chan error, 1)
	go func() {
		_, err := a.Invoke(context.Background(), "echo", map[string]any{"text": "test"})
		result <- err
	}()
	m := readMessage(t, p)
	_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: m.ID, ConnectionID: id, Result: map[string]any{"text": 42}})
	if err := <-result; err == nil || !strings.HasPrefix(err.Error(), "INVALID_RESULT") {
		t.Fatalf("invalid result: %v", err)
	}
	_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: "oversized", ConnectionID: id, Result: strings.Repeat("x", bridge.MaxMessage)})
	p.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := p.ReadJSON(&m); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversized close: %v", err)
	}
	eventually(t, func() bool { return !a.Snapshot().PageConnected })
}

func TestCanceledQueuedCallIsNeverSent(t *testing.T) {
	a, url := testBridge(t, 0)
	p, id := connectPage(t, url)
	registerPage(t, p, id, testDef("echo"))
	firstDone := make(chan error, 1)
	go func() {
		_, err := a.Invoke(context.Background(), "echo", map[string]any{"text": "first"})
		firstDone <- err
	}()
	first := readMessage(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := a.Invoke(ctx, "echo", map[string]any{"text": "second"})
		secondDone <- err
	}()
	eventually(t, func() bool { return a.Snapshot().QueuedRequests == 1 })
	cancel()
	if err := <-secondDone; err == nil || !strings.HasPrefix(err.Error(), "CANCELED") {
		t.Fatal(err)
	}
	_ = p.WriteJSON(bridge.Message{Version: 1, Type: "tool_result", ID: first.ID, ConnectionID: id, Result: map[string]any{"text": "first"}})
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return a.Snapshot().PendingRequests == 0 })
	p.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var m bridge.Message
	if err := p.ReadJSON(&m); err == nil {
		t.Fatalf("canceled job sent to page: %+v", m)
	}
}
