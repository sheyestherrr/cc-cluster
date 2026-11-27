// 各页共用的骨架：页头、卡片标题、徽标、出错说明、页签。
// 页头与卡片的样式类取自 Resin（MIT）的样式表。
import { AlertTriangle, ChevronLeft } from "lucide-react";
import type React from "react";
import { Badge, type BadgeVariant } from "@/components/ui/card";
import { cn } from "@/lib/utils";

/** 跟在名称后面的备注；没有备注时什么也不显示。 */
export function NoteText({ note, className }: { note?: string; className?: string }) {
  if (!note) return null;
  return <span title={note} className={cn("min-w-0 shrink truncate text-[12.5px] font-normal text-muted", className)}>{note}</span>;
}

/**
 * 卡片的标题：名称是打开详情页的链接，备注跟在后面。
 * 卡片本身只响应鼠标点击，不是按钮：键盘与读屏软件经由这个链接打开详情，卡片里的菜单与按钮不再嵌在另一个可交互元素中。
 */
export function TileTitle({ href, name, note, label }: { href: string; name: string; note?: string; label: string }) {
  return (
    <p title={note}>
      <a className="tile-link" href={href} aria-label={label}>{name}</a>
      <NoteText note={note} className="ml-2 font-sans text-[12px]" />
    </p>
  );
}

/** 卡片外层的属性：整张响应鼠标点击，里面的按钮、菜单与链接除外。 */
export function tileProps(onOpen: () => void) {
  return {
    className: "tile",
    onClick: (e: React.MouseEvent) => !(e.target as HTMLElement).closest("button, a, [data-menu]") && onOpen(),
  };
}

interface PageHeaderProps {
  title: string;
  /** 详情页里对象的备注，与标题同一行。 */
  note?: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  /** 详情页左上角的“返回”。 */
  back?: { label: string; onClick: () => void };
}

export function PageHeader({ title, note, subtitle, actions, back }: PageHeaderProps) {
  return (
    <header className="module-header">
      <div className="min-w-0">
        {back && (
          <button type="button" className="back-link mb-1.5" onClick={back.onClick}>
            <ChevronLeft size={15} />
            {back.label}
          </button>
        )}
        <div className="flex min-w-0 items-baseline gap-3">
          <h1 className={cn("min-w-0 truncate", note && "max-w-[70%] shrink-0")}>{title}</h1>
          <NoteText note={note} className="text-[14px]" />
        </div>
        {subtitle && <p className="module-description">{subtitle}</p>}
      </div>
      {actions && <div className="module-actions">{actions}</div>}
    </header>
  );
}

/** 一页的内容：从上到下排开的若干块。 */
export function Page({ children, className }: { children: React.ReactNode; className?: string }) {
  return <section className={cn("page", className)}>{children}</section>;
}

const toneVariant: Record<string, BadgeVariant> = { warn: "warning", env: "info", danger: "danger", ok: "success" };

export function Tag({ children, tone, title }: { children: React.ReactNode; tone?: "warn" | "env" | "danger" | "ok"; title?: string }) {
  return (
    <Badge title={title} variant={tone ? toneVariant[tone] : "neutral"} className="max-w-[220px] shrink-0 truncate">
      {children}
    </Badge>
  );
}

/** 卡片页脚与详情页头部的一项：名称在前（可省略），取值在后。 */
export function Fact({ k, children }: { k?: string; children: React.ReactNode }) {
  return (
    <span className="platform-fact">
      {k && <span>{k}</span>}
      <strong>{children}</strong>
    </span>
  );
}

/** 留在页面上的一条说明：失败的操作（说明在上，出错之前的进度在下），或需要留意的事项（tone="warning"）。 */
export function ErrorBox({ text, log, onClose, tone = "error" }: { text: string; log?: string[]; onClose?: () => void; tone?: "error" | "warning" }) {
  return (
    <div className={cn("callout", tone === "warning" ? "callout-warning" : "callout-error")} role={tone === "warning" ? "status" : "alert"}>
      <AlertTriangle size={15} />
      <div className="min-w-0 flex-1">
        <span className="whitespace-pre-wrap break-words">{text}</span>
        {!!log?.length && <pre className="mono mt-2 whitespace-pre-wrap break-words text-[12px] leading-[18px] opacity-80">{log.join("\n")}</pre>}
      </div>
      {onClose && <button type="button" onClick={onClose} className="shrink-0 border-0 bg-transparent text-[13px] font-semibold underline-offset-2 hover:underline">关闭</button>}
    </div>
  );
}

/**
 * 分段的页签：只有选中的一项在 Tab 顺序里，左右方向键与 Home、End 在各项之间移动并选中。
 * panel 是它切换的那块内容的 id，内容的外层用 tabPanelProps 取得对应的属性。
 */
export function Tabs<T extends string>({ value, onChange, items, label, panel }: { value: T; onChange: (v: T) => void; items: [T, string][]; label: string; panel: string }) {
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const at = items.findIndex(([k]) => k === value);
    const next = e.key === "ArrowRight" ? at + 1 : e.key === "ArrowLeft" ? at - 1 : e.key === "Home" ? 0 : e.key === "End" ? items.length - 1 : -2;
    if (next === -2) return;
    e.preventDefault();
    const to = (next + items.length) % items.length;
    onChange(items[to][0]);
    e.currentTarget.querySelectorAll<HTMLButtonElement>("[role=tab]")[to]?.focus();
  };
  return (
    <div className="segmented-control" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
      {items.map(([k, text]) => (
        <button key={k} type="button" role="tab" id={`${panel}-${k}`} aria-controls={panel} aria-selected={value === k} tabIndex={value === k ? 0 : -1} className="segmented-control-option" onClick={() => onChange(k)}>
          <span>{text}</span>
        </button>
      ))}
    </div>
  );
}

/** 页签切换的那块内容外层的属性。 */
export const tabPanelProps = (panel: string, tab: string) => ({ role: "tabpanel", id: panel, "aria-labelledby": `${panel}-${tab}` }) as const;
