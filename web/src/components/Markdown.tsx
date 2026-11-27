// 把更新说明里的 Markdown 渲染成元素。解析在 lib/markdown.ts；这里不用 dangerouslySetInnerHTML。
import { Fragment } from "react";
import { parseMarkdown, type Inline } from "@/lib/markdown";

function Inlines({ items }: { items: Inline[] }) {
  return (
    <>
      {items.map((n, i) => {
        switch (n.t) {
          case "code":
            return <code key={i}>{n.v}</code>;
          case "bold":
            return (
              <strong key={i}>
                <Inlines items={n.c} />
              </strong>
            );
          case "link":
            return (
              <a key={i} href={n.href} target="_blank" rel="noopener noreferrer">
                <Inlines items={n.c} />
              </a>
            );
          default:
            return <Fragment key={i}>{n.v}</Fragment>;
        }
      })}
    </>
  );
}

export function Markdown({ source }: { source: string }) {
  return (
    <div className="md">
      {parseMarkdown(source).map((b, i) => {
        switch (b.t) {
          case "heading":
            return (
              <h4 key={i}>
                <Inlines items={b.c} />
              </h4>
            );
          case "list": {
            const List = b.ordered ? "ol" : "ul";
            return (
              <List key={i}>
                {b.items.map((item, j) => (
                  <li key={j} style={item.depth ? { marginLeft: item.depth * 18 } : undefined}>
                    <Inlines items={item.c} />
                  </li>
                ))}
              </List>
            );
          }
          case "code":
            return <pre key={i}>{b.v}</pre>;
          case "rule":
            return <hr key={i} />;
          default:
            return (
              <p key={i}>
                <Inlines items={b.c} />
              </p>
            );
        }
      })}
    </div>
  );
}
