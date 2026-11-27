// 表单的小部件：带标签的一格、带说明的开关。样式类取自 Resin（MIT）的 form-grid / field-group / subscription-switch-item。
import type React from "react";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { ID_HINT } from "@/lib/api";
import { cn } from "@/lib/utils";

/** 两列的表单网格；窄屏时变成一列。 */
export function FormGrid({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("form-grid !mt-0", className)}>{children}</div>;
}

/** 带标签的一格。标签用 <label> 包住控件：点击标签即聚焦，读屏软件能读出控件的名称。 */
export function Field({ label, hint, warn, wide, required, children }: { label: string; hint?: React.ReactNode; warn?: React.ReactNode; wide?: boolean; required?: boolean; children: React.ReactNode }) {
  return (
    <label className={cn("field-group", wide && "field-span-2")}>
      <span className="field-label">
        {label}
        {required && <span className="field-required" title="必填" aria-hidden="true"> *</span>}
      </span>
      {children}
      {hint && <span className="field-hint !mt-0 block">{hint}</span>}
      {warn && <span className="field-hint !mt-0 block !text-warn">{warn}</span>}
    </label>
  );
}

/** 名称（槽位、网关、节点、订阅）的规则，与服务端的校验一致。 */
const ID_PATTERN = "[a-z0-9][a-z0-9\\-]{0,30}";

/** 名称输入框：必填；离开输入框后不符合规则时标出，提交时由浏览器指出原因。规则写在所在 Field 的说明里。 */
export function NameInput(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <Input required pattern={ID_PATTERN} maxLength={31} spellCheck={false} autoCapitalize="off" autoComplete="off" title={ID_HINT} {...props} />;
}

/**
 * 几种取值的写法，与服务端的校验一致，展开到输入框上：离开输入框后不符合时标出，提交时由浏览器指出原因（title 是它显示的说明）。
 * 取值范围（地址池须为私有网段、内存不小于下限等）仍由服务端判断。
 */
export const FORMAT = {
  tz: { pattern: "[A-Za-z0-9_+\\-]+(/[A-Za-z0-9_+\\-]+){0,2}", title: "时区名称，例如 Asia/Tokyo" },
  memory: { pattern: "[0-9]+(\\.[0-9]+)?[kKmMgG]?", title: "数字加单位 k、m 或 g，例如 8g" },
  cidr: { pattern: "[0-9]{1,3}(\\.[0-9]{1,3}){3}/[0-9]{1,2}", title: "IPv4 网段，例如 10.213.250.0/24" },
  ipv4: { pattern: "[0-9]{1,3}(\\.[0-9]{1,3}){3}", title: "IPv4 地址，例如 198.51.100.20" },
} as const;

export function SwitchItem({ label, hint, checked, onChange, wide, disabled }: { label: string; hint?: React.ReactNode; checked: boolean; onChange: (v: boolean) => void; wide?: boolean; disabled?: boolean }) {
  return (
    <div className={cn("subscription-switch-item", wide && "field-span-2")}>
      <div className="min-w-0">
        <div className="field-label">{label}</div>
        {hint && <p className="field-hint !mt-0.5">{hint}</p>}
      </div>
      <Switch checked={checked} onCheckedChange={onChange} label={label} disabled={disabled} />
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
