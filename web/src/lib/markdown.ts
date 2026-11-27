// 更新说明用的一小部分 Markdown：标题、列表、段落、代码块、分隔线，行内的粗体、代码和链接。
// 只解析成结构，由组件渲染成元素；不生成 HTML 字符串，写的人塞不进标签和脚本。
// 认不出的写法原样当作文字。

export type Inline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "bold"; c: Inline[] }
  | { t: "link"; href: string; c: Inline[] };

export interface ListItem {
  /** 缩进的层数，从 0 起。 */
  depth: number;
  c: Inline[];
}

export type Block =
  | { t: "heading"; level: number; c: Inline[] }
  | { t: "para"; c: Inline[] }
  | { t: "list"; ordered: boolean; items: ListItem[] }
  | { t: "code"; v: string }
  | { t: "rule" };

const INLINE = /`([^`]+)`|\*\*(.+?)\*\*|\[([^\]]+)\]\(([^)\s]+)\)/g;

/** 只有 http(s) 的地址才做成链接；别的（javascript: 之类）只留下文字。 */
export const safeHref = (href: string) => /^https?:\/\//i.test(href);

export function parseInline(src: string): Inline[] {
  const out: Inline[] = [];
  const text = (v: string) => {
    if (!v) return;
    const last = out[out.length - 1];
    if (last?.t === "text") last.v += v;
    else out.push({ t: "text", v });
  };
  let at = 0;
  for (const m of src.matchAll(INLINE)) {
    text(src.slice(at, m.index));
    at = m.index + m[0].length;
    if (m[1] !== undefined) out.push({ t: "code", v: m[1] });
    else if (m[2] !== undefined) out.push({ t: "bold", c: parseInline(m[2]) });
    else if (safeHref(m[4])) out.push({ t: "link", href: m[4], c: parseInline(m[3]) });
    else text(m[3]);
  }
  text(src.slice(at));
  return out;
}

// 中文的字和标点（含全角）。
const CJK = /[　-〿一-鿿＀-￯]/;

/** 把换了行的一段话接起来：两边都是中文时不加空格。 */
function joinLines(a: string, b: string): string {
  if (!a) return b;
  return CJK.test(a[a.length - 1]) || CJK.test(b[0] ?? "") ? a + b : `${a} ${b}`;
}

const BULLET = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;

export function parseMarkdown(src: string): Block[] {
  const blocks: Block[] = [];
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  let para = "";
  let list: { ordered: boolean; items: { depth: number; text: string }[] } | null = null;
  const flush = () => {
    if (para) blocks.push({ t: "para", c: parseInline(para) });
    if (list) blocks.push({ t: "list", ordered: list.ordered, items: list.items.map((i) => ({ depth: i.depth, c: parseInline(i.text) })) });
    para = "";
    list = null;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      flush();
      const body: string[] = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) body.push(lines[i]);
      blocks.push({ t: "code", v: body.join("\n") });
      continue;
    }
    if (!line.trim()) {
      flush();
      continue;
    }
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flush();
      blocks.push({ t: "rule" });
      continue;
    }
    const h = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (h) {
      flush();
      blocks.push({ t: "heading", level: h[1].length, c: parseInline(h[2]) });
      continue;
    }
    const b = BULLET.exec(line);
    if (b) {
      const ordered = /\d/.test(b[2]);
      const depth = Math.min(3, Math.floor(b[1].replace(/\t/g, "  ").length / 2));
      if (para || (list && depth === 0 && list.ordered !== ordered)) flush();
      list ??= { ordered, items: [] };
      list.items.push({ depth, text: b[3] });
      continue;
    }
    if (list && /^\s+/.test(line)) {
      // 列表项换行后缩进着接下去的内容。
      const last = list.items[list.items.length - 1];
      last.text = joinLines(last.text, line.trim());
      continue;
    }
    if (list) flush();
    para = joinLines(para, line.trim());
  }
  flush();
  return blocks;
}
