// 取自 Claude-Hydra（MIT）app/src/components/PageHeader.tsx，v1.1.6；
// 另加了 Line、KeyValue、Tag、ErrorBox 几个小组件（Line 与 KeyValue 出自上游的 EnvironmentDialog.tsx、SettingsPage.tsx，
// Tag 出自 AccountsPage.tsx）。
import type React from "react";
import { cn } from "@/lib/utils";

interface PageHeaderProps {
  /** Kept for accessibility; the active tab already shows the page name. */
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
}

/** One quiet intro line under the tab bar, with page actions on the right. */
export function PageHeader({ title, subtitle, actions }: PageHeaderProps) {
  return (
    <header className="flex min-h-[36px] items-center justify-between gap-4 px-5 pb-3">
      <h1 className="sr-only">{title}</h1>
      <p className="min-w-0 text-[13px] leading-5 text-fg-2">{subtitle}</p>
      {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
    </header>
  );
}

/**
 * Content column shared by every page.
 *
 * 给了 side 时变成两栏：左边是列表，右边是所选对象的详情，各自滚动；
 * 窗口不够宽时详情排到列表下面。没有 side 时是一条限宽的单栏。
 */
export function Page({ children, side }: { children: React.ReactNode; side?: React.ReactNode }) {
  if (side === undefined) return <div className="view-in w-full max-w-[860px] pb-8">{children}</div>;
  return (
    <div className="view-in w-full min-[1080px]:grid min-[1080px]:h-full min-[1080px]:grid-cols-[minmax(420px,5fr)_minmax(380px,6fr)]">
      <div className="min-w-0 pb-8 min-[1080px]:overflow-y-auto">{children}</div>
      <aside
        aria-label="详情"
        className="min-w-0 border-line px-5 pb-8 max-[1079px]:mx-5 max-[1079px]:border-t-[0.5px] max-[1079px]:px-0 max-[1079px]:pt-5 min-[1080px]:overflow-y-auto min-[1080px]:border-l-[0.5px]"
      >
        {side}
      </aside>
    </div>
  );
}

/** 详情栏的抬头：名字、状态和主要操作。 */
export function DetailHeader({ badge, title, status, actions }: { badge: React.ReactNode; title: string; status: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <header className="flex min-h-[48px] items-center gap-3 pb-4">
      {badge}
      <div className="min-w-0 flex-1">
        <h2 className="truncate text-[15px] font-semibold tracking-[-0.01em]">{title}</h2>
        <div className="mt-0.5 flex items-center gap-1.5 text-[12px] text-muted">{status}</div>
      </div>
      {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
    </header>
  );
}

/** 详情栏里的各个分组：够宽时排成两列。 */
export function DetailGrid({ children }: { children: React.ReactNode }) {
  return <div className="grid animate-fade-in items-start gap-x-5 gap-y-5 [grid-template-columns:repeat(auto-fit,minmax(min(100%,330px),1fr))]">{children}</div>;
}

/**
 * 列表里可以被选中的一行用的属性。用鼠标点行上任何不是按钮的地方都算选中；
 * 键盘用户经行里的标题（RowTitle，一个真正的按钮）选中。选中的行左边有一道标记。
 */
export function selectableRow(selected: boolean, onSelect: () => void) {
  return {
    onClick: (e: React.MouseEvent) => {
      if (!(e.target as HTMLElement).closest("button, a, input, [role=menu]")) onSelect();
    },
    className: cn(
      "group relative flex items-center gap-3 px-4 py-3",
      "before:pointer-events-none before:absolute before:inset-y-2.5 before:left-0 before:w-[3px] before:rounded-r-full before:bg-brand before:transition-opacity before:duration-150 motion-reduce:before:transition-none",
      selected ? "bg-card-2 before:opacity-100" : "before:opacity-0 hover:bg-card-2",
    ),
  };
}

/** 行的标题：点它选中这一行，在右边的详情栏里显示。 */
export function RowTitle({ selected, onSelect, children }: { selected: boolean; onSelect: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onSelect}
      aria-pressed={selected}
      title="在右边显示详情"
      className="min-w-0 truncate rounded-sm text-left text-[13.5px] font-medium text-fg active:!scale-100"
    >
      {children}
    </button>
  );
}

/** 没有可显示的对象时，详情栏里的一句说明。 */
export function DetailEmpty({ children }: { children: React.ReactNode }) {
  return <div className="flex h-full min-h-[160px] items-center justify-center px-6 text-center text-[12.5px] leading-5 text-muted">{children}</div>;
}

/** A labelled group: small uppercase label above a grouped list card. */
export function Group({ label, action, children, className }: { label?: string; action?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={className}>
      {(label || action) && (
        <div className="flex items-end justify-between">
          {label ? <div className="group-label">{label}</div> : <span />}
          {action && <div className="pb-1">{action}</div>}
        </div>
      )}
      <div className="list-card">{children}</div>
    </section>
  );
}

/** A settings-style row inside a list card: label and hint on the left, control on the right. */
export function Row({ label, hint, children }: { label: React.ReactNode; hint?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6 px-4 py-3">
      <div className="min-w-0">
        <div className="text-[13px] font-medium">{label}</div>
        {hint && <div className="mt-0.5 text-[12px] leading-[18px] text-muted">{hint}</div>}
      </div>
      {children !== undefined && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </div>
  );
}

/** 表单里的一行：左边是标签，右边是控件。 */
export function Line({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[88px_1fr] items-center gap-3 px-4 py-2">
      <span className="text-[12.5px] text-fg-2">{label}</span>
      <div className="flex min-w-0 items-center gap-2">{children}</div>
    </div>
  );
}

export function KeyValue({ k, v, plain, children }: { k: string; v?: string | null; plain?: boolean; children?: React.ReactNode }) {
  return (
    <div className="flex gap-4 px-4 py-2.5 text-[12.5px]">
      <span className="w-20 shrink-0 text-muted">{k}</span>
      {children ?? (
        <span className={cn("selectable min-w-0 break-all text-fg-2", plain ? "leading-[18px]" : "font-mono text-[11.5px] leading-[18px]")}>{v || "—"}</span>
      )}
    </div>
  );
}

export function Tag({ children, tone, title }: { children: React.ReactNode; tone?: "warn" | "env" | "danger" | "ok"; title?: string }) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex h-[18px] max-w-[200px] shrink-0 items-center truncate rounded-full px-1.5 text-[10.5px] font-medium",
        tone === "danger" ? "bg-danger-soft text-danger"
          : tone === "warn" ? "bg-warn-soft text-warn"
          : tone === "env" ? "bg-brand-soft text-brand"
          : tone === "ok" ? "bg-ok-soft text-ok"
          : "bg-pill text-fg-2",
      )}
    >
      {children}
    </span>
  );
}

/** 一次失败的操作：说明在上，出错之前的进度在下。 */
export function ErrorBox({ text, log, onClose }: { text: string; log?: string[]; onClose?: () => void }) {
  return (
    <div className="selectable rounded-xl bg-danger-soft px-4 py-3 text-[13px] text-danger">
      <div className="flex items-start justify-between gap-3">
        <span className="min-w-0 whitespace-pre-wrap break-words">{text}</span>
        {onClose && <button onClick={onClose} className="shrink-0 text-[12px] underline-offset-2 hover:underline">知道了</button>}
      </div>
      {!!log?.length && <pre className="mt-2 whitespace-pre-wrap break-words font-mono text-[11.5px] leading-[18px] opacity-80">{log.join("\n")}</pre>}
    </div>
  );
}
