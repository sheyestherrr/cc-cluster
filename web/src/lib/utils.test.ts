import assert from "node:assert/strict";
import { test } from "node:test";

import { PALETTE, badgeLetter, bytesText, colorIndex, formatBytes, imageName, rateText, relativeTime, spanText } from "./utils.ts";

test("imageName 去掉仓库前缀", () => {
  assert.equal(imageName("ghcr.io/sheyestherrr/cc-cluster-gateway:v0.0.1"), "cc-cluster-gateway:v0.0.1");
  assert.equal(imageName("cc-cluster-gateway:dev"), "cc-cluster-gateway:dev");
  assert.equal(imageName("localhost:5000/worker"), "worker");
  assert.equal(imageName(`reg.example.com/a/worker@sha256:${"0123456789ab".repeat(5)}0123`), "worker@sha256:0123456789ab…");
  assert.equal(imageName(), "");
});

test("bytesText 按大小换单位", () => {
  assert.equal(bytesText(), "0 B");
  assert.equal(bytesText(812), "812 B");
  assert.equal(bytesText(1024), "1 KB");
  assert.equal(bytesText(1536), "1.5 KB");
  assert.equal(bytesText(120 * 2 ** 20), "120 MB");
  assert.equal(bytesText(2.5 * 2 ** 30), "2.5 GB");
  // 超过最大的单位就不再往上换。
  assert.equal(bytesText(5 * 2 ** 50), "5120 TB");
  assert.equal(rateText(2048), "2 KB/s");
});

test("spanText 写成秒、分钟、小时、天", () => {
  assert.equal(spanText(-5), "0 秒");
  assert.equal(spanText(8_000), "8 秒");
  assert.equal(spanText(300_000), "5 分钟");
  assert.equal(spanText(9_000_000), "2.5 小时");
  assert.equal(spanText(172_800_000), "2 天");
});

test("formatBytes 用于内存大小，没有值时留空", () => {
  assert.equal(formatBytes(), "");
  assert.equal(formatBytes(0), "");
  assert.equal(formatBytes(512 * 2 ** 20), "512 MiB");
  assert.equal(formatBytes(8 * 2 ** 30), "8 GiB");
});

test("relativeTime 相对现在的说法", () => {
  const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
  assert.equal(relativeTime(), "从未");
  assert.equal(relativeTime(null), "从未");
  assert.equal(relativeTime("不是时间"), "从未");
  assert.equal(relativeTime(ago(10_000)), "刚刚");
  assert.equal(relativeTime(ago(5 * 60_000)), "5 分钟前");
  assert.equal(relativeTime(ago(3 * 3_600_000)), "3 小时前");
  assert.equal(relativeTime(ago(2 * 86_400_000)), "2 天前");
});

test("badgeLetter 取第一个字母或数字", () => {
  assert.equal(badgeLetter("work"), "W");
  assert.equal(badgeLetter("-az1"), "A");
  assert.equal(badgeLetter("中转"), "中");
});

test("colorIndex 对同一个名字稳定，且落在调色板里", () => {
  assert.equal(colorIndex("work"), colorIndex("work"));
  for (const name of ["work", "research", "中转", ""]) {
    const i = colorIndex(name);
    assert.ok(Number.isInteger(i) && i >= 0 && i < PALETTE.length, `${name} → ${i}`);
  }
});
