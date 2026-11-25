import assert from "node:assert/strict";
import { test } from "node:test";

import { opDetail, opName } from "./ops.ts";

test("opName 把操作名换成中文", () => {
  assert.equal(opName("slot.up"), "启动槽位");
  assert.equal(opName("cluster.images"), "更换镜像");
  assert.equal(opName("watch"), "持续检测");
});

test("opName 认得改备注这一类", () => {
  assert.equal(opName("slot.note"), "槽位备注");
  assert.equal(opName("nodes.note"), "节点备注");
});

test("opName 不认识的原样显示", () => {
  assert.equal(opName("slot.unknown"), "slot.unknown");
  assert.equal(opName("thing.note"), "thing.note");
  assert.equal(opName("weird"), "weird");
});

test("opDetail 翻译节点的用途，别的原样", () => {
  assert.equal(opDetail("landing"), "落地");
  assert.equal(opDetail("transit"), "中转");
  assert.equal(opDetail("work"), "work");
  assert.equal(opDetail(), "");
});
