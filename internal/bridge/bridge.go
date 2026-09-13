package bridge

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Options configures the call deadline and registration notification.
// OnChange runs while the registry is locked and must not re-enter Bridge.
type Options struct {
	Timeout  time.Duration
	OnChange func([]ToolDef)
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

func New(options Options) *Bridge {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	b := &Bridge{tools: map[string]compiledTool{}, pending: map[string]pendingRequest{}, queue: make(chan *job, 100), stop: make(chan struct{}), timeout: timeout, onChange: options.OnChange}
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
	return nil
}

func (b *Bridge) deliver(p *page, m Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != p || m.ConnectionID != p.id {
		return
	}
	if pending, ok := b.pending[m.ID]; ok && pending.p == p {
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

// State is a consistent snapshot used by health reporting.
type State struct {
	PageConnected   bool
	Ready           bool
	ToolCount       int
	PendingRequests int
	QueuedRequests  int
}

func (b *Bridge) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return State{PageConnected: b.active != nil, Ready: b.ready, ToolCount: len(b.tools), PendingRequests: len(b.pending) + len(b.queue), QueuedRequests: len(b.queue)}
}
