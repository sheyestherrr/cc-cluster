// 用编出来的数据预览网页界面：起一个只在本机的模拟接口，配上构建好的前端。
// 不启动 cc-cluster，不碰 Docker，不访问外网；页面上的操作都不会生效。
// 由 scripts/preview.sh 调用；直接运行时先 make web，再在仓库根目录执行 node scripts/preview.mjs。
import fs from "node:fs";
import http from "node:http";
import path from "node:path";

const DIST = path.resolve("internal/web/dist");
const FIRST_PORT = Number(process.env.PORT) || 7878;
const FIXED = Boolean(process.env.PORT);

if (!fs.existsSync(path.join(DIST, "index.html"))) {
  console.error("没有找到构建好的前端（internal/web/dist）：先执行 make web，或用 sh scripts/preview.sh");
  process.exit(1);
}

// ---- 模拟数据 ----
const now = Date.now();
const ago = (min) => new Date(now - min * 60000).toISOString();
let seed = 7;
const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);

const slot = (id, landing, machine, extra) => ({
  id, gateway: "main", landing, state: "ready", hostname: `user-${machine}`, user: "user", home: "/home/user", mounts: [{ source: "/data/projects/shop", target: "/data/projects/shop" }],
  enter_command: `cc-cluster enter ${id}`, applied_at: ago(180), memory_bytes: 8 * 2 ** 30, cpus: 4, tz: "Asia/Tokyo", ...extra,
});
const report = (ip, cc, country, flags = []) => ({ ip, checked_at: ago(200), sources: ["a", "b", "c"], country, country_code: cc, city: "", asn: 64500, as_org: "Example Broadband", residential: true, mobile: false, trust_score: 82, fraud_score: 12, flags });
const state = {
  version: "v0.0.1", data_dir: "/home/kevin/.cc-cluster", cluster: { name: "cc-kevin", gateway_image: "ghcr.io/sheyestherrr/cc-cluster-gateway:v0.0.1", worker_image: "ghcr.io/sheyestherrr/cc-cluster-worker:v0.0.1" }, exit_report_auto: true, landing_limit: 5,
  gateways: [{ id: "main", transit: "hk", transit_backups: ["sg"], active_transit: "hk", egress_network: "cc-kevin-egress", pool: "10.213.250.0/24", container: "running", core_ready: true, config_current: true, slots: ["work", "research", "frontend", "spare"] }],
  nodes: [
    { id: "home-jp", note: "东京家宽", role: "landing", type: "socks5", server: "198.51.100.20", port: 1080, used_by: ["work"], region: "JP", region_from: "exit", tz: "Asia/Tokyo", exit_report: report("198.51.100.20", "JP", "日本") },
    { id: "home-us", role: "landing", type: "socks5", server: "198.51.100.31", port: 1080, used_by: ["research"], region: "US", region_from: "exit", exit_report: report("198.51.100.31", "US", "美国", [{ name: "代理", by: ["a"], of: 3 }]) },
    { id: "home-de", role: "landing", type: "http", server: "198.51.100.44", port: 8080, used_by: ["frontend"], region: "DE", region_from: "exit", exit_report: report("198.51.100.44", "DE", "德国") },
    { id: "home-gb", role: "landing", type: "socks5", server: "198.51.100.57", port: 1080, used_by: ["spare"], region: "GB", region_from: "name" },
    { id: "hk", role: "transit", type: "trojan", server: "hk.example.com", port: 443, used_by: ["main"], source: { sub: "air", name: "🇭🇰 香港 01" }, region: "HK", region_from: "name" },
    { id: "sg", role: "transit", type: "hysteria2", server: "sg.example.com", port: 443, used_by: ["main"], source: { sub: "air", name: "🇸🇬 新加坡 02" }, region: "SG", region_from: "name" },
  ],
  slots: [
    slot("work", "home-jp", "ThinkPad-T14-Gen-3", { note: "主力", exit_ip: "198.51.100.20", exit_where: "日本 东京", watch: { at: ago(3), since: ago(900), status: "ok", exit_ip: "198.51.100.20" } }),
    slot("research", "home-us", "OptiPlex-7090", { exit_ip: "198.51.100.31", exit_where: "美国 洛杉矶", watch: { at: ago(3), since: ago(300), status: "ok", exit_ip: "198.51.100.31" } }),
    slot("frontend", "home-de", "Laptop-13", { exit_ip: "198.51.100.44", exit_where: "德国 法兰克福", watch: { at: ago(3), since: ago(25), status: "unreachable", detail: "经链路访问不了探测地址" } }),
    slot("spare", "home-gb", "XPS-15-9530", { state: "stopped", exit_ip: "", exit_where: "" }),
  ],
  subs: [
    { id: "air", note: "机场", count: 30, fetched_at: ago(240), tested_at: ago(40), usable: 21, format: "clash", every_min: 720, checked_at: ago(240) },
    { id: "mine", count: 6, fetched_at: ago(2000), tested_at: ago(300), usable: 4, local: true, format: "links", skipped: 1 },
  ],
};

const regions = ["HK", "HK", "HK", "JP", "JP", "SG", "SG", "US", "US", "TW", "KR", "DE", "GB"];
const names = { HK: "🇭🇰 香港", JP: "🇯🇵 日本", SG: "🇸🇬 新加坡", US: "🇺🇸 美国", TW: "🇹🇼 台湾", KR: "🇰🇷 韩国", DE: "🇩🇪 德国", GB: "🇬🇧 英国" };
const subNodes = (n, okRatio) => {
  const out = [];
  for (let i = 0; i < n; i++) {
    const r = regions[i % regions.length];
    const ok = rnd() < okRatio;
    out.push({ name: `${names[r]} ${String(i + 1).padStart(2, "0")}`, type: ["trojan", "hysteria2", "vless"][i % 3], server: `${r.toLowerCase()}${i}.example.com:443`, delay_ms: ok ? Math.round(60 + rnd() * rnd() * 1300) : -1, region: r, nodes: i === 0 ? ["hk"] : i === 5 ? ["sg"] : undefined });
  }
  out.push({ name: "剩余流量：120 GB", type: "trojan", server: "info.example.com:443", delay_ms: 0, info: true });
  const rank = (d) => (d > 0 ? d : 1e9 + (d < 0 ? 1 : 0));
  return out.sort((a, b) => rank(a.delay_ms) - rank(b.delay_ms));
};
const nodesBySub = { air: subNodes(30, 0.7), mine: subNodes(6, 0.7) };

function history(hours) {
  const step = hours <= 24 ? 300 : hours <= 168 ? 3600 : 21600;
  const to = now;
  const from = Math.floor((to - hours * 3600e3) / (step * 1000)) * step * 1000;
  const n = Math.floor((to - from) / (step * 1000)) + 1;
  const traffic = [];
  const totals = { work: { up: 0, down: 0, conns: 0 }, research: { up: 0, down: 0, conns: 0 }, frontend: { up: 0, down: 0, conns: 0 } };
  const weight = { work: 1, research: 0.45, frontend: 0.2 };
  let burst = 0;
  for (let i = 0; i < n; i++) {
    const t = from + i * step * 1000;
    const hour = new Date(t).getHours() + new Date(t).getMinutes() / 60;
    const day = Math.max(0, Math.sin(((hour - 8) / 14) * Math.PI)); // 白天干活
    burst = burst * 0.82 + (rnd() < 0.08 ? rnd() * 5 : 0);
    const p = { t: new Date(t).toISOString(), up: 0, down: 0, conns: 0, slots: {} };
    for (const [id, w] of Object.entries(weight)) {
      if (id === "frontend" && t > now - 25 * 60000) continue; // 连不上之后没有流量
      const level = (0.15 + day * (0.7 + rnd() * 0.6) + burst) * w * (step / 300);
      const down = Math.round(level * 9e6 * (0.6 + rnd() * 0.8));
      const up = Math.round(down * (0.06 + rnd() * 0.07));
      const conns = Math.round(2 + day * 9 * w + rnd() * 3);
      p.slots[id] = { up, down, conns };
      p.up += up;
      p.down += down;
      p.conns += conns;
      totals[id].up += up;
      totals[id].down += down;
      totals[id].conns = Math.max(totals[id].conns, conns);
    }
    traffic.push(p);
  }
  const iso = (ms) => new Date(Math.max(ms, from)).toISOString();
  const span = hours * 60;
  const watch = {
    work: [{ from: iso(from), to: iso(now - 3 * 60000), status: "ok", exit_ip: "198.51.100.20", checks: Math.round(span / 5) }],
    research: [
      { from: iso(from), to: iso(now - span * 0.42 * 60000), status: "ok", exit_ip: "198.51.100.31", checks: 120 },
      { from: iso(now - span * 0.42 * 60000), to: iso(now - span * 0.4 * 60000), status: "changed", exit_ip: "203.0.113.77", checks: 2 },
      { from: iso(now - span * 0.36 * 60000), to: iso(now - 3 * 60000), status: "ok", exit_ip: "198.51.100.31", checks: 90 },
    ],
    frontend: [
      { from: iso(from + span * 0.1 * 60000), to: iso(now - span * 0.7 * 60000), status: "ok", exit_ip: "198.51.100.44", checks: 60 },
      { from: iso(now - span * 0.7 * 60000), to: iso(now - span * 0.66 * 60000), status: "unreachable", checks: 6 },
      { from: iso(now - span * 0.66 * 60000), to: iso(now - 25 * 60000), status: "ok", exit_ip: "198.51.100.44", checks: 150 },
      { from: iso(now - 25 * 60000), to: iso(now - 3 * 60000), status: "unreachable", checks: 5 },
    ],
  };
  const pts = (base, total, k) => Array.from({ length: k }, (_, i) => ({ t: iso(from + ((i + 0.6) / k) * (to - from)), usable: Math.max(0, Math.round(base + Math.sin(i * 1.7) * 3 - (i === 2 ? 6 : 0))), total }));
  return { from: iso(from), to: new Date(to).toISOString(), step, traffic, totals, watch, subs: { air: pts(20, 30, hours <= 24 ? 5 : 12), mine: pts(4, 6, 3) } };
}

const hosts = ["api.anthropic.com", "statsig.anthropic.com", "github.com", "objects.githubusercontent.com", "registry.npmjs.org", "pypi.org", "files.pythonhosted.org", "sentry.io", "api.github.com", "proxy.golang.org"];
const conns = hosts.slice(0, 7).map((h, i) => ({ id: `c${i}`, net: "tcp", host: h, port: 443, up: Math.round(rnd() * 4e5), down: Math.round(rnd() * rnd() * 6e7), start: ago(rnd() * 40 + 0.2), chain: "home-jp" }));
const connlog = {
  from: ago(1440), to: ago(0), total: 418,
  records: Array.from({ length: 60 }, (_, i) => ({ slot: "work", start: ago(i * 6 + 9), end: ago(i * 6 + 2), net: "tcp", host: hosts[i % hosts.length], port: 443, up: Math.round(rnd() * 2e5), down: Math.round(rnd() * rnd() * 3e7), chain: "home-jp" })),
  hosts: hosts.map((h, i) => ({ host: h, count: Math.round(160 / (i + 1)) + 2, up: Math.round(4e7 / (i + 1)), down: Math.round(2.4e9 / (i + 1) ** 1.6), last: ago(i * 13 + 1) })),
};
const ops = [
  ["slot.up", "work", true, "出口 198.51.100.20"], ["sub.test", "air", true, "30 个节点里 21 个测通"], ["watch", "frontend", true, "连不上：经链路访问不了探测地址"], ["sub.refresh", "air", true, "30 个节点"],
  ["gateway.transits", "main", true, "中转 hk，候选 sg"], ["slot.up", "research", true, "出口 198.51.100.31"], ["node.add", "home-gb", true, "落地 socks5"], ["slot.up", "spare", false, "落地 home-gb 连不上"],
].map(([op, target, ok, text], i) => ({ time: ago(i * 37 + 4), op, target, ok, ...(ok ? { detail: text } : { error: text }) }));

const routes = (url) => {
  const p = url.pathname.replace(/^\/api\//, "");
  if (p === "state") return state;
  if (p === "ops") return ops;
  if (p === "doctor") return [{ name: "Docker", ok: true, detail: "可用（模拟）" }, { name: "网关镜像", ok: true, detail: state.cluster.gateway_image }, { name: "worker 镜像", ok: true, detail: state.cluster.worker_image }];
  if (p === "traffic") return { at: ago(0.1), recording: true, slots: { work: { up_bps: 41200, down_bps: 1.84e6, up: 4.1e8, down: 7.9e9, conns: 7 }, research: { up_bps: 2100, down_bps: 96000, up: 9e7, down: 2.2e9, conns: 3 }, frontend: { up_bps: 0, down_bps: 0, up: 3e7, down: 5e8, conns: 0 } } };
  if (p === "history") return history(Number(url.searchParams.get("hours")) || 24);
  let m;
  if ((m = /^subs\/([^/]+)\/nodes$/.exec(p))) return nodesBySub[m[1]] ?? [];
  if ((m = /^slots\/([^/]+)\/connections$/.exec(p))) return m[1] === "work" ? conns : [];
  if ((m = /^slots\/([^/]+)\/connlog$/.exec(p))) return m[1] === "work" ? connlog : { from: ago(1440), to: ago(0), total: 0, records: [], hosts: [] };
  return undefined;
};

// ---- 服务 ----
const types = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".woff": "font/woff", ".woff2": "font/woff2" };
const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://127.0.0.1");
  if (url.pathname.startsWith("/api/")) {
    // 读取的接口返回编好的数据；其余（各种操作）一律说明这是预览。
    const data = req.method === "GET" ? routes(url) : undefined;
    res.writeHead(data === undefined ? 404 : 200, { "Content-Type": "application/json; charset=utf-8" });
    res.end(JSON.stringify(data === undefined ? { error: "这是模拟数据的预览，操作不会生效" } : { data, log: [] }));
    return;
  }
  const file = path.join(DIST, url.pathname === "/" ? "index.html" : decodeURIComponent(url.pathname));
  if (!file.startsWith(DIST + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
    res.writeHead(404).end();
    return;
  }
  res.writeHead(200, { "Content-Type": types[path.extname(file)] ?? "application/octet-stream" });
  fs.createReadStream(file).pipe(res);
});

const listen = (port) => new Promise((ok, bad) => {
  server.once("error", bad);
  server.listen(port, "127.0.0.1", () => (server.off("error", bad), ok()));
});
// 没有用 PORT 指定端口时，被占用就往后找：真的网页界面可能正开在 7878 上。
let port = FIRST_PORT;
for (;;) {
  try {
    await listen(port);
    break;
  } catch (e) {
    if (e.code !== "EADDRINUSE" || FIXED || port >= FIRST_PORT + 20) {
      console.error(`监听 127.0.0.1:${port} 失败：${e.message}；可以用 PORT=端口 换一个`);
      process.exit(1);
    }
    port++;
  }
}
console.log(`模拟数据的预览（数据是编的，页面上的操作都不会生效）：\n\n  http://127.0.0.1:${port}/#token=preview\n\n只监听本机。在别的机器上看时先做端口转发：ssh -L ${port}:127.0.0.1:${port} <这台机器>\n按 Ctrl-C 停止。`);
