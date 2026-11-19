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

/** Content column shared by every page. */
export function Page({ children }: { children: React.ReactNode }) {
  return <div className="view-in w-full max-w-[860px] pb-8">{children}</div>;
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

export function KeyValue({ k, v }: { k: string; v?: string | null }) {
  return (
    <div className="flex gap-4 px-4 py-2.5 text-[12.5px]">
      <span className="w-24 shrink-0 text-muted">{k}</span>
      <span className="selectable min-w-0 break-all font-mono text-[11.5px] leading-[18px] text-fg-2">{v || "—"}</span>
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
