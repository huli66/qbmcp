package bridge

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

func (b *Bridge) ServeWS(w http.ResponseWriter, r *http.Request, originOK func(string) bool) {
	u := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return originOK(r.Header.Get("Origin")) }}
	c, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	p := &page{id: randomID(), conn: c, done: make(chan struct{})}
	defer c.Close()
	c.SetReadLimit(MaxMessage)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello Message
	if err = c.ReadJSON(&hello); err != nil || hello.Type != "hello" || hello.Version != 1 {
		p.close(websocket.CloseProtocolError, "invalid hello")
		return
	}
	if !b.attach(p) {
		p.close(1001, "service stopped")
		return
	}
	defer func() {
		b.mu.Lock()
		b.detachLocked(p, failure("PAGE_DISCONNECTED", "网页连接已断开"))
		b.mu.Unlock()
	}()
	if err = p.send(Message{Type: "hello_ack", ConnectionID: p.id}); err != nil {
		return
	}
	c.SetReadDeadline(time.Now().Add(45 * time.Second))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-t.C:
				if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					c.Close()
					return
				}
			}
		}
	}()
	for {
		var m Message
		if err = c.ReadJSON(&m); err != nil {
			return
		}
		if m.Version != 1 || m.ConnectionID != p.id {
			_ = p.send(Message{Type: "error", ID: m.ID, Error: failure("INVALID_MESSAGE", "版本或连接 ID 不正确")})
			continue
		}
		switch m.Type {
		case "register_tools":
			if err := b.register(p, m); err != nil {
				e := err.(*BridgeError)
				_ = p.send(Message{Type: "error", ID: m.ID, Error: e})
			} else {
				b.mu.Lock()
				v := b.schemaVersion
				b.mu.Unlock()
				_ = p.send(Message{Type: "register_ack", ID: m.ID, ConnectionID: p.id, SchemaVersion: v})
			}
		case "tool_result", "error":
			b.deliver(p, m)
		default:
			_ = p.send(Message{Type: "error", ID: m.ID, Error: failure("INVALID_MESSAGE", "未知消息类型")})
		}
	}
}
