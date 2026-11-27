import assert from "node:assert/strict";
import { test } from "node:test";

import { parseInline, parseMarkdown } from "./markdown.ts";

test("行内：粗体、代码、链接，粗体里可以有代码", () => {
  assert.deepEqual(parseInline("**`cc-cluster web` 默认在后台运行**：打印地址"), [
    { t: "bold", c: [{ t: "code", v: "cc-cluster web" }, { t: "text", v: " 默认在后台运行" }] },
    { t: "text", v: "：打印地址" },
  ]);
  assert.deepEqual(parseInline("取自 [Resin](https://github.com/Resinat/Resin)（MIT）"), [
    { t: "text", v: "取自 " },
    { t: "link", href: "https://github.com/Resinat/Resin", c: [{ t: "text", v: "Resin" }] },
    { t: "text", v: "（MIT）" },
  ]);
  // 代码里的星号不当作粗体；落单的星号和反引号原样留着。
  assert.deepEqual(parseInline("`a ** b` 和 2 * 3 ` 4"), [{ t: "code", v: "a ** b" }, { t: "text", v: " 和 2 * 3 ` 4" }]);
});

test("行内：不是 http(s) 的链接只留文字，标签原样当文字", () => {
  assert.deepEqual(parseInline("[点我](javascript:alert(1))"), [{ t: "text", v: "点我)" }]);
  assert.deepEqual(parseInline("[x](data:text/html,hi)"), [{ t: "text", v: "x" }]);
  assert.deepEqual(parseInline('<img src=x onerror="alert(1)">'), [{ t: "text", v: '<img src=x onerror="alert(1)">' }]);
});

test("块：发布说明的样子", () => {
  const blocks = parseMarkdown(
    ["日常用法收成两条命令。", "", "### 主要功能", "", "- **槽位**：一个容器", "- 固定出口", "  - 子项", "", "升级：重新运行", "安装命令。", "", "---", "", "English"].join("\r\n"),
  );
  assert.deepEqual(
    blocks.map((b) => b.t),
    ["para", "heading", "list", "para", "rule", "para"],
  );
  const [, heading, list, para] = blocks;
  assert.deepEqual(heading, { t: "heading", level: 3, c: [{ t: "text", v: "主要功能" }] });
  assert.ok(list.t === "list" && !list.ordered);
  assert.deepEqual(
    list.items.map((i) => i.depth),
    [0, 0, 1],
  );
  assert.deepEqual(list.items[0].c[0], { t: "bold", c: [{ t: "text", v: "槽位" }] });
  // 换了行的一段话接起来，中文之间不加空格。
  assert.deepEqual(para, { t: "para", c: [{ t: "text", v: "升级：重新运行安装命令。" }] });
});

test("块：代码块、有序列表、列表项的续行", () => {
  const blocks = parseMarkdown(["1. 第一步", "   接着写", "2. second", "   line", "", "```sh", "cc-cluster update", "# 不是标题", "```", "after code"].join("\n"));
  assert.deepEqual(blocks, [
    {
      t: "list",
      ordered: true,
      items: [
        { depth: 0, c: [{ t: "text", v: "第一步接着写" }] },
        { depth: 0, c: [{ t: "text", v: "second line" }] },
      ],
    },
    { t: "code", v: "cc-cluster update\n# 不是标题" },
    { t: "para", c: [{ t: "text", v: "after code" }] },
  ]);
  assert.deepEqual(parseMarkdown(""), []);
  assert.deepEqual(parseMarkdown("  \n\n"), []);
});
