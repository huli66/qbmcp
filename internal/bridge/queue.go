package bridge

import (
	"context"
	"errors"
)

func (b *Bridge) Invoke(ctx context.Context, name string, args map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	b.mu.Lock()
	p := b.active
	if p == nil {
		b.mu.Unlock()
		return nil, failure("PAGE_NOT_CONNECTED", "请打开网页并连接服务")
	}
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
	m := Message{Type: "call_tool", ID: randomID(), ConnectionID: p.id, SchemaVersion: b.schemaVersion, Name: name, Arguments: args}
	j := &job{ctx: ctx, p: p, m: m, output: t.output, result: make(chan reply, 1)}
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
	if j.m.SchemaVersion != b.schemaVersion {
		b.mu.Unlock()
		return reply{err: failure("SCHEMA_CHANGED", "工具定义已变化，请刷新工具列表后调用")}
	}
	rc := make(chan reply, 1)
	b.pending[j.m.ID] = pendingRequest{j.p, rc}
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
