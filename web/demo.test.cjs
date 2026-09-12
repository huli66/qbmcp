// Optional development checks: node --test web/demo.test.cjs
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, 'demo.html'), 'utf8').match(/<script>([\s\S]*?)<\/script>/)[1];

function page() {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, { textContent: '', value: '' });
    return elements.get(id);
  };
  const sockets = [], timers = new Map(), fetches = [];
  class WebSocket {
    static OPEN = 1;
    readyState = 1;
    sent = [];
    constructor(url) { this.url = url; sockets.push(this); }
    send(raw) { this.sent.push(JSON.parse(raw)); }
    close(code = 1000) { this.readyState = 3; this.onclose?.({ code }); }
    receive(message) { this.onmessage({ data: JSON.stringify(message) }); }
  }
  vm.runInNewContext(source, {
    document: { getElementById: element },
    location: { origin: 'http://127.0.0.1:32300' },
    WebSocket, AbortController,
    setTimeout: (fn, delay) => { const id = timers.size + 1; timers.set(id, { fn, delay }); return id; },
    clearTimeout: id => timers.delete(id),
    fetch: async (url, options) => {
      fetches.push({ url, options });
      return { ok: true, json: async () => ({ items: ['Go', 'MCP'], source: 'test' }) };
    },
  });
  element('token').value = 'test-token';
  element('connect').onclick();
  const socket = sockets[0];
  socket.onopen();
  socket.receive({ type: 'hello_ack', connectionId: 'connection-1' });
  return { element, sockets, socket, timers, fetches };
}
const settle = () => new Promise(resolve => setImmediate(resolve));

test('handshake, schemas, API and DOM handlers', async () => {
  const p = page();
  assert.equal(p.socket.url, 'ws://127.0.0.1:32300/ws');
  assert.equal(p.socket.sent[0].token, 'test-token');
  const registration = p.socket.sent[1];
  assert.equal(registration.type, 'register_tools');
  assert.equal(registration.tools.length, 2);
  p.socket.receive({ type: 'call_tool', id: 'api', name: 'demo_load_data', arguments: {} });
  p.socket.receive({ type: 'call_tool', id: 'dom', name: 'demo_set_text', arguments: { text: '<script>literal</script>' } });
  await settle();
  assert.equal(p.fetches[0].url, '/demo/api/data');
  const results = p.socket.sent.filter(m => m.type === 'tool_result');
  assert.deepEqual(results.map(m => m.id), ['api', 'dom']);
  assert.equal(results[0].result.source, 'test');
  assert.equal(p.element('target').textContent, '<script>literal</script>');
  assert.equal(results[1].connectionId, 'connection-1');
  p.socket.receive({ type: 'request_tools', id: 'refresh' });
  assert.equal(p.socket.sent.at(-1).id, 'refresh');
});

test('queued cancellation does not modify DOM', async () => {
  const p = page();
  p.element('target').textContent = 'unchanged';
  p.socket.receive({ type: 'call_tool', id: 'cancel-me', name: 'demo_set_text', arguments: { text: 'changed' } });
  p.socket.receive({ type: 'cancel', id: 'cancel-me' });
  await settle();
  assert.equal(p.element('target').textContent, 'unchanged');
  assert.equal(p.socket.sent.filter(m => m.type === 'tool_result').length, 0);
});

test('replacement and authentication failures do not reconnect', () => {
  for (const code of [4001, 4003]) {
    const p = page();
    p.socket.close(code);
    assert.equal(p.timers.size, 0);
    assert.match(p.element('status').textContent, code === 4001 ? /替换/ : /认证失败/);
  }
  const p = page();
  p.socket.close(1006);
  assert.equal([...p.timers.values()][0].delay, 1000);
});

test('manual reconnect cancels old queued DOM work', async () => {
  const p = page();
  p.element('target').textContent = 'unchanged';
  p.socket.receive({ type: 'call_tool', id: 'old', name: 'demo_set_text', arguments: { text: 'changed' } });
  p.element('connect').onclick();
  await settle();
  assert.equal(p.element('target').textContent, 'unchanged');
});
