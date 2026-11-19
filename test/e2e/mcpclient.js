// 端到端测试用的最小 MCP 客户端：在 worker 里启动 tmux-mcp-rs，经标准输入输出做一轮固定的调用，
// 把结果作为一行 JSON 打印出来。调用的内容是写死的（列出、查看、执行 hostname），不经过任何模型。
// 用法：node mcpclient.js <socket> <paneId>
const { spawn } = require('child_process');
const [sock, pane] = process.argv.slice(2);

const p = spawn('tmux-mcp-rs', ['--socket', sock, '--config', '/etc/tmux-mcp/config.toml'], { stdio: ['pipe', 'pipe', 'ignore'] });
let buf = '';
let seq = 0;
const waiting = new Map();
p.stdout.on('data', (d) => {
  buf += d;
  for (let i = buf.indexOf('\n'); i >= 0; i = buf.indexOf('\n')) {
    const line = buf.slice(0, i);
    buf = buf.slice(i + 1);
    let m;
    try { m = JSON.parse(line); } catch { continue; }
    if (m.id && waiting.has(m.id)) {
      waiting.get(m.id)(m);
      waiting.delete(m.id);
    }
  }
});

function call(method, params) {
  return new Promise((resolve, reject) => {
    const id = ++seq;
    waiting.set(id, resolve);
    p.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
    setTimeout(() => reject(new Error('等不到应答：' + method)), 20000);
  });
}

async function tool(name, args) {
  const r = await call('tools/call', { name, arguments: args });
  if (r.error) return { isError: true, text: JSON.stringify(r.error) };
  return { isError: !!r.result.isError, text: (r.result.content || []).map((c) => c.text || '').join(' ') };
}

(async () => {
  await call('initialize', { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'e2e', version: '0' } });
  p.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');
  const out = {};
  out.tools = (await call('tools/list', {})).result.tools.map((t) => t.name);
  out.sessions = await tool('list-sessions', {});
  out.otherSocket = await tool('list-sessions', { socket: '/tmp/not-allowed.sock' });
  out.exec = await tool('execute-command', { paneId: pane, command: 'hostname' });
  const m = /"commandId":\s*"([^"]+)"/.exec(out.exec.text);
  if (m) out.result = await tool('get-command-result', { commandId: m[1], waitMs: 10000 });
  out.screen = await tool('capture-pane', { paneId: pane, lines: 20 });
  console.log(JSON.stringify(out));
  p.kill();
  process.exit(0);
})().catch((e) => {
  console.log(JSON.stringify({ fatal: String(e) }));
  p.kill();
  process.exit(1);
});
