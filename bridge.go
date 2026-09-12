package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const maxMessage = 4 << 20

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
type compiledTool struct {
	def           ToolDef
	input, output *jsonschema.Schema
}
type Message struct {
	Version       int            `json:"version"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	Token         string         `json:"token,omitempty"`
	PageID        string         `json:"pageId,omitempty"`
	ConnectionID  string         `json:"connectionId,omitempty"`
	SchemaVersion uint64         `json:"schemaVersion,omitempty"`
	Tools         []ToolDef      `json:"tools,omitempty"`
	Name          string         `json:"name,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	Result        any            `json:"result,omitempty"`
	Error         *BridgeError   `json:"error,omitempty"`
}
type page struct {
	id      string
	conn    *websocket.Conn
	writeMu sync.Mutex
	done    chan struct{}
	reason  *BridgeError // written before done is closed
}

func (p *page) send(m Message) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select {
	case <-p.done:
		return p.reason
	default:
	}
	p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	m.Version = 1
	return p.conn.WriteJSON(m)
}
func (p *page) close(code int, reason string) {
	_ = p.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = p.conn.Close()
}

type reply struct {
	value any
	err   error
}
type job struct {
	ctx    context.Context
	p      *page
	m      Message
	output *jsonschema.Schema
	result chan reply
}
type pendingRequest struct {
	p      *page
	kind   string
	result chan reply
}
type Bridge struct {
	mu            sync.Mutex
	active        *page
	tools         map[string]compiledTool
	schemaVersion uint64
	ready         bool
	pending       map[string]pendingRequest
	queue         chan *job
	stop          chan struct{}
	wg            sync.WaitGroup
	onChange      func([]ToolDef) // called under mu; must not re-enter Bridge
	timeout       time.Duration
}

func newBridge() *Bridge {
	b := &Bridge{tools: map[string]compiledTool{}, pending: map[string]pendingRequest{}, queue: make(chan *job, 100), stop: make(chan struct{}), timeout: 30 * time.Second}
	b.wg.Add(1)
	go b.worker()
	return b
}
func (b *Bridge) Close() {
	b.mu.Lock()
	select {
	case <-b.stop:
		b.mu.Unlock()
		return
	default:
		close(b.stop)
	}
	p := b.active
	if p != nil {
		b.detachLocked(p, failure("SERVICE_STOPPED", "服务已停止"))
	}
	b.mu.Unlock()
	if p != nil {
		p.close(1001, "service stopped")
	}
	b.wg.Wait()
}
func (b *Bridge) detachLocked(p *page, err *BridgeError) {
	select {
	case <-p.done:
		return
	default:
		p.reason = err
		close(p.done)
	}
	if b.active == p {
		b.active = nil
		b.ready = false
		b.tools = map[string]compiledTool{}
		b.schemaVersion++
		if b.onChange != nil {
			b.onChange(nil)
		}
	}
}
func (b *Bridge) attach(p *page) bool {
	b.mu.Lock()
	select {
	case <-b.stop:
		b.mu.Unlock()
		return false
	default:
	}
	old := b.active
	if old != nil {
		b.detachLocked(old, failure("PAGE_REPLACED", "网页已被新连接替换"))
	}
	b.active = p
	b.mu.Unlock()
	if old != nil {
		old.close(4001, "page replaced; do not reconnect")
	}
	return true
}

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("schema 仅允许内部引用，不加载外部资源")
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

func compileSchema(doc map[string]any) (*jsonschema.Schema, error) {
	if doc == nil || doc["type"] != "object" {
		return nil, errors.New("schema 根类型必须是 object")
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(denyLoader{})
	if err := c.AddResource("https://qbmcp.invalid/schema", doc); err != nil {
		return nil, err
	}
	return c.Compile("https://qbmcp.invalid/schema")
}
func compileTools(defs []ToolDef) (map[string]compiledTool, error) {
	if len(defs) > 256 {
		return nil, errors.New("最多注册 256 个工具")
	}
	result := make(map[string]compiledTool, len(defs))
	for _, d := range defs {
		if !toolName.MatchString(d.Name) || strings.HasPrefix(d.Name, "qbmcp_") {
			return nil, fmt.Errorf("非法或保留的工具名: %s", d.Name)
		}
		if _, ok := result[d.Name]; ok {
			return nil, fmt.Errorf("重复工具名: %s", d.Name)
		}
		if d.Kind != "api" && d.Kind != "dom" {
			return nil, errors.New("kind 必须为 api 或 dom")
		}
		in, err := compileSchema(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("%s inputSchema: %w", d.Name, err)
		}
		var out *jsonschema.Schema
		if d.OutputSchema != nil {
			out, err = compileSchema(d.OutputSchema)
			if err != nil {
				return nil, fmt.Errorf("%s outputSchema: %w", d.Name, err)
			}
		}
		result[d.Name] = compiledTool{d, in, out}
	}
	return result, nil
}
func (b *Bridge) register(p *page, m Message) error {
	compiled, err := compileTools(m.Tools)
	if err != nil {
		return failure("INVALID_SCHEMA", err.Error())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != p {
		return failure("PAGE_REPLACED", "连接已失效")
	}
	b.tools = compiled
	b.ready = true
	b.schemaVersion++
	if b.onChange != nil {
		b.onChange(m.Tools)
	}
	if pending, ok := b.pending[m.ID]; ok && pending.p == p && pending.kind == "request_tools" {
		select {
		case pending.result <- reply{value: m.Tools}:
		default:
		}
	}
	return nil
}
func (b *Bridge) deliver(p *page, m Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != p || m.ConnectionID != p.id {
		return
	}
	if pending, ok := b.pending[m.ID]; ok && pending.p == p && (pending.kind == "call_tool" || m.Error != nil) {
		r := reply{value: m.Result}
		if m.Error != nil {
			r.err = m.Error
		}
		select {
		case pending.result <- r:
		default:
		}
	}
}
func (b *Bridge) invoke(ctx context.Context, name string, args map[string]any, discover bool) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	b.mu.Lock()
	p := b.active
	if p == nil {
		b.mu.Unlock()
		return nil, failure("PAGE_NOT_CONNECTED", "请打开网页并连接服务")
	}
	m := Message{Type: "request_tools", ID: randomID(), ConnectionID: p.id, SchemaVersion: b.schemaVersion}
	var output *jsonschema.Schema
	if !discover {
		t, ok := b.tools[name]
		if !ok {
			b.mu.Unlock()
			return nil, failure("UNKNOWN_TOOL", "当前网页未注册该工具")
		}
		if args == nil {
			args = map[string]any{}
		}
		if err := t.input.Validate(args); err != nil {
			b.mu.Unlock()
			return nil, failure("INVALID_ARGUMENTS", err.Error())
		}
		m.Type = "call_tool"
		m.Name = name
		m.Arguments = args
		output = t.output
	}
	j := &job{ctx: ctx, p: p, m: m, output: output, result: make(chan reply, 1)}
	select {
	case b.queue <- j:
	default:
		b.mu.Unlock()
		return nil, failure("QUEUE_FULL", "等待队列已满")
	}
	b.mu.Unlock()
	select {
	case r := <-j.result:
		return r.value, r.err
	case <-p.done:
		return nil, p.reason
	case <-ctx.Done():
		return nil, contextError(ctx)
	}
}
func contextError(ctx context.Context) *BridgeError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failure("TIMEOUT", "调用超时；已发生的操作不会回滚")
	}
	return failure("CANCELED", "调用已取消；已发生的操作不会回滚")
}
func (b *Bridge) worker() {
	defer b.wg.Done()
	for {
		select {
		case <-b.stop:
			return
		case j := <-b.queue:
			j.result <- b.execute(j)
		}
	}
}
func (b *Bridge) execute(j *job) reply {
	if j.ctx.Err() != nil {
		return reply{err: contextError(j.ctx)}
	}
	b.mu.Lock()
	if b.active != j.p {
		b.mu.Unlock()
		return reply{err: failure("PAGE_REPLACED", "原网页连接已失效")}
	}
	if j.m.Type == "call_tool" && j.m.SchemaVersion != b.schemaVersion {
		b.mu.Unlock()
		return reply{err: failure("SCHEMA_CHANGED", "工具定义已变化，请重新发现后调用")}
	}
	rc := make(chan reply, 1)
	b.pending[j.m.ID] = pendingRequest{j.p, j.m.Type, rc}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, j.m.ID); b.mu.Unlock() }()
	if err := j.p.send(j.m); err != nil {
		return reply{err: failure("PAGE_DISCONNECTED", "发送请求失败")}
	}
	select {
	case r := <-rc:
		select {
		case <-j.p.done:
			return reply{err: j.p.reason}
		default:
		}
		if r.err == nil && j.output != nil {
			if err := j.output.Validate(r.value); err != nil {
				return reply{err: failure("INVALID_RESULT", err.Error())}
			}
		}
		return r
	case <-j.p.done:
		return reply{err: j.p.reason}
	case <-j.ctx.Done():
		_ = j.p.send(Message{Type: "cancel", ID: j.m.ID, ConnectionID: j.p.id})
		return reply{err: contextError(j.ctx)}
	}
}

func (b *Bridge) serveWS(w http.ResponseWriter, r *http.Request, token string, originOK func(string) bool) {
	u := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return originOK(r.Header.Get("Origin")) }}
	c, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	p := &page{id: randomID(), conn: c, done: make(chan struct{})}
	defer c.Close()
	c.SetReadLimit(maxMessage)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello Message
	if err = c.ReadJSON(&hello); err != nil || hello.Type != "hello" || hello.Version != 1 || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) != 1 {
		p.close(4003, "authentication failed")
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
				b.deliver(p, Message{ID: m.ID, ConnectionID: p.id, Error: e})
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

// Normalize through JSON so schema validation sees only JSON values.
func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, failure("INVALID_ARGUMENTS", "参数必须是 JSON object")
	}
	return args, nil
}
