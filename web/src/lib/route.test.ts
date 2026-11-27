import assert from "node:assert/strict";
import { test } from "node:test";

import { parseRoute, routeHash } from "./route.ts";

test("parseRoute 识别页面与其下的对象", () => {
  assert.deepEqual(parseRoute("#/slots"), { page: "slots", path: [] });
  assert.deepEqual(parseRoute("#/slots/work"), { page: "slots", path: ["work"] });
  assert.deepEqual(parseRoute("#/network/node/hk"), { page: "network", path: ["node", "hk"] });
  assert.deepEqual(parseRoute("#/subs/%E6%9C%BA%E5%9C%BA"), { page: "subs", path: ["机场"] });
});

test("parseRoute 对空的、无法识别的 hash 返回总览", () => {
  for (const hash of ["", "#", "#/", "#/nope/x", "#token=abc123", "#/slots%"]) {
    assert.deepEqual(parseRoute(hash), { page: "overview", path: [] }, hash);
  }
  // 解码失败的一段原样保留，不抛错。
  assert.deepEqual(parseRoute("#/slots/%E0%A4%A"), { page: "slots", path: ["%E0%A4%A"] });
});

test("routeHash 与 parseRoute 互逆", () => {
  assert.equal(routeHash("overview"), "#/overview");
  assert.equal(routeHash("network", ["gateway", "main"]), "#/network/gateway/main");
  assert.deepEqual(parseRoute(routeHash("subs", ["a/b 机场"])), { page: "subs", path: ["a/b 机场"] });
});
