import assert from "node:assert/strict";
import { test } from "node:test";

// api.ts 在加载时就从地址里读令牌，所以先摆好浏览器那几个全局对象，再加载它。
const TOKEN = "0123456789abcdef0123456789abcdef";
const stored = new Map<string, string>();
let addressBar = `/#token=${TOKEN}`;
let sent: { url: string; headers: Record<string, string> } = { url: "", headers: {} };
let respond: () => Response = () => Response.json({ data: null });

const fake: Record<string, unknown> = {
  window: { location: { hash: `#token=${TOKEN}`, pathname: "/", search: "" } },
  localStorage: {
    getItem: (k: string) => stored.get(k) ?? null,
    setItem: (k: string, v: string) => void stored.set(k, v),
  },
  history: {
    replaceState: (_state: unknown, _title: string, url: string) => {
      addressBar = url;
    },
  },
  fetch: async (url: string, init: { headers: Record<string, string> }) => {
    sent = { url, headers: init.headers };
    return respond();
  },
};
for (const [name, value] of Object.entries(fake)) {
  Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
}

const { ApiError, api, errorText, isUnauthorized } = await import("./api.ts");

test("地址里的令牌被记进浏览器，并从地址栏去掉", () => {
  assert.equal(stored.get("cc-cluster-token"), TOKEN);
  assert.equal(addressBar, "/");
});

test("请求带着令牌，成功时取出 data", async () => {
  respond = () => Response.json({ data: [{ op: "slot.up" }] });
  assert.deepEqual(await api.ops(), [{ op: "slot.up" }]);
  assert.equal(sent.url, "./api/ops");
  assert.equal(sent.headers["X-CC-Token"], TOKEN);
});

test("401 被认作“没有令牌或令牌不对”，说明取自接口", async () => {
  respond = () => Response.json({ error: "访问令牌不对" }, { status: 401 });
  const err = await api.state().catch((e: unknown) => e);
  assert.ok(err instanceof ApiError);
  assert.equal(err.status, 401);
  assert.ok(isUnauthorized(err));
  assert.equal(errorText(err), "访问令牌不对");
});

test("别的错误不算没有令牌，出错前的进度带在 log 里", async () => {
  respond = () => Response.json({ error: "槽位不存在", log: ["检查网关"] }, { status: 404 });
  const err = await api.state().catch((e: unknown) => e);
  assert.ok(err instanceof ApiError);
  assert.ok(!isUnauthorized(err));
  assert.deepEqual(err.log, ["检查网关"]);
});

test("返回的不是 JSON 时保留状态码", async () => {
  respond = () => new Response("<html>", { status: 502 });
  const err = await api.state().catch((e: unknown) => e);
  assert.ok(err instanceof ApiError);
  assert.equal(err.status, 502);
});

test("连不上时状态码是 0，并提示重新启动", async () => {
  respond = () => {
    throw new TypeError("fetch failed");
  };
  const err = await api.state().catch((e: unknown) => e);
  assert.ok(err instanceof ApiError);
  assert.equal(err.status, 0);
  assert.ok(!isUnauthorized(err));
  assert.match(err.message, /cc-cluster web/);
});
