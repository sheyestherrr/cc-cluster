// 各页共用的骨架：页头、分组卡片、设置行、键值、徽标、出错说明。
// 页头与卡片的样式类取自 Resin（MIT）的样式表；Group / Row / Line / KeyValue / Tag 这些名字沿用本项目原有的。
import { AlertTriangle, ChevronLeft } from "lucide-react";
import type React from "react";
import { Badge, type BadgeVariant } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface PageHeaderProps {
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  /** 详情页左上角的“返回”。 */
  back?: { label: string; onClick: () => void };
}

export function PageHeader({ title, subtitle, actions, back }: PageHeaderProps) {
  return (
    <header className="module-header">
      <div className="min-w-0">
        {back && (
          <button type="button" className="back-link mb-1.5" onClick={back.onClick}>
            <ChevronLeft size={15} />
            {back.label}
          </button>
        )}
        <h2 className="truncate">{title}</h2>
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

/** 带标题的一组：标题在上，下面是一张分行的卡片。 */
export function Group({ label, action, children, className }: { label?: string; action?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={className}>
      {(label || action) && (
        <div className="flex items-end justify-between gap-3">
          {label ? <div className="group-label">{label}</div> : <span />}
          {action && <div className="pb-2">{action}</div>}
        </div>
      )}
      <div className="list-card">{children}</div>
    </section>
  );
}

/** 卡片里的一行设置：左边是名称与说明，右边是控件。 */
export function Row({ label, hint, children }: { label: React.ReactNode; hint?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6 px-4 py-3.5">
      <div className="min-w-0">
        <div className="text-[14px] font-semibold text-fg">{label}</div>
        {hint && <div className="mt-0.5 text-[12.5px] leading-[19px] text-muted">{hint}</div>}
      </div>
      {children !== undefined && <div className="flex shrink-0 items-center gap-2">{children}</div>}
    </div>
  );
}

/** 表单里的一行：左边是标签，右边是控件。 */
export function Line({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[96px_1fr] items-center gap-3 px-4 py-2.5">
      <span className="field-label">{label}</span>
      <div className="flex min-w-0 items-center gap-2">{children}</div>
    </div>
  );
}

export function KeyValue({ k, v, plain, children }: { k: string; v?: string | null; plain?: boolean; children?: React.ReactNode }) {
  return (
    <div className="flex gap-4 px-4 py-3 text-[13px]">
      <span className="w-24 shrink-0 text-muted">{k}</span>
      {children ?? <span className={cn("min-w-0 break-all text-fg", !plain && "mono")}>{v || "—"}</span>}
    </div>
  );
}

const toneVariant: Record<string, BadgeVariant> = { warn: "warning", env: "info", danger: "danger", ok: "success" };

export function Tag({ children, tone, title }: { children: React.ReactNode; tone?: "warn" | "env" | "danger" | "ok"; title?: string }) {
  return (
    <Badge title={title} variant={tone ? toneVariant[tone] : "neutral"} className="max-w-[220px] shrink-0 truncate">
      {children}
    </Badge>
  );
}

/** 一次失败的操作：说明在上，出错之前的进度在下。 */
export function ErrorBox({ text, log, onClose }: { text: string; log?: string[]; onClose?: () => void }) {
  return (
    <div className="callout callout-error" role="alert">
      <AlertTriangle size={15} />
      <div className="min-w-0 flex-1">
        <span className="whitespace-pre-wrap break-words">{text}</span>
        {!!log?.length && <pre className="mono mt-2 whitespace-pre-wrap break-words text-[12px] leading-[18px] opacity-80">{log.join("\n")}</pre>}
      </div>
      {onClose && <button type="button" onClick={onClose} className="shrink-0 border-0 bg-transparent text-[13px] font-semibold underline-offset-2 hover:underline">知道了</button>}
    </div>
  );
}

/** 没有内容时占位的一句话。 */
export function Empty({ icon, children, className }: { icon?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("empty-box plain", className)}>
      {icon}
      <p>{children}</p>
    </div>
  );
}

/** 分段的页签。 */
export function Tabs<T extends string>({ value, onChange, items, label }: { value: T; onChange: (v: T) => void; items: [T, string][]; label: string }) {
  return (
    <div className="segmented-control" role="tablist" aria-label={label}>
      {items.map(([k, text]) => (
        <button key={k} type="button" role="tab" aria-selected={value === k} className="segmented-control-option" onClick={() => onChange(k)}>
          <span>{text}</span>
        </button>
      ))}
    </div>
  );
}
