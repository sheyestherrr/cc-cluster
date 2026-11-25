// 表单的小部件：带标签的一格、带说明的开关。样式类取自 Resin（MIT）的 form-grid / field-group / subscription-switch-item。
import type React from "react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

/** 两列的表单网格；窄屏时变成一列。 */
export function FormGrid({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("form-grid !mt-0", className)}>{children}</div>;
}

export function Field({ label, hint, warn, wide, children }: { label: string; hint?: React.ReactNode; warn?: React.ReactNode; wide?: boolean; children: React.ReactNode }) {
  return (
    <div className={cn("field-group", wide && "field-span-2")}>
      <span className="field-label">{label}</span>
      {children}
      {hint && <p className="field-hint !mt-0">{hint}</p>}
      {warn && <p className="field-hint !mt-0 !text-warn">{warn}</p>}
    </div>
  );
}

export function SwitchItem({ label, hint, checked, onChange, wide }: { label: string; hint?: React.ReactNode; checked: boolean; onChange: (v: boolean) => void; wide?: boolean }) {
  return (
    <div className={cn("subscription-switch-item", wide && "field-span-2")}>
      <div className="min-w-0">
        <div className="field-label">{label}</div>
        {hint && <p className="field-hint !mt-0.5">{hint}</p>}
      </div>
      <Switch checked={checked} onCheckedChange={onChange} label={label} />
    </div>
  );
}

/** 表单里的一节：小标题加说明。 */
export function FormSection({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="platform-drawer-section-head">
        <h4>{title}</h4>
        {hint && <p>{hint}</p>}
      </div>
      {children}
    </section>
  );
}
