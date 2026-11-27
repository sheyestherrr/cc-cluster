// 表单的小部件：带标签的一格、带说明的开关。样式类取自 Resin（MIT）的 form-grid / field-group / subscription-switch-item。
import { AlertTriangle } from "lucide-react";
import type React from "react";
import { useId } from "react";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { ID_HINT } from "@/lib/api";
import { cn } from "@/lib/utils";

/** 两列的表单网格；窄屏时变成一列。 */
export function FormGrid({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("form-grid !mt-0", className)}>{children}</div>;
}

/**
 * 带标签的一格。标签用 <label> 包住控件：点击标签即聚焦，读屏软件能读出控件的名称。
 * error 是控件填写后不符合规则时显示的说明（样式见 app.css 的 .field-error）：只靠边框变红无法说明错在哪里。
 * 它与控件的关联（aria-invalid、aria-describedby）由 lib/form-validity.ts 维护。
 */
export function Field({ label, hint, warn, error, wide, required, children }: { label: string; hint?: React.ReactNode; warn?: React.ReactNode; error?: string; wide?: boolean; required?: boolean; children: React.ReactNode }) {
  const errorId = useId();
  return (
    <label className={cn("field-group", wide && "field-span-2")}>
      <span className="field-label">
        {label}
        {required && <span className="field-required" title="必填" aria-hidden="true"> *</span>}
      </span>
      {children}
      {hint && <span className="field-hint !mt-0 block">{hint}</span>}
      {warn && <span className="field-hint !mt-0 block !text-warn">{warn}</span>}
      {error && (
        <span id={errorId} className="field-error">
          <AlertTriangle size={13} role="img" aria-label="错误" />
          {error}
        </span>
      )}
    </label>
  );
}

/** 各项填写不符合规则时的说明，传给 Field 的 error。 */
export const ERRORS = {
  id: "名称须以小写字母或数字开头，只含小写字母、数字与连字符，不超过 31 个字符。",
  tz: "时区格式有误，应为 Asia/Tokyo 这样的名称。",
  memory: "内存上限格式有误，应为数字加单位 k、m 或 g，例如 8g。",
  cidr: "地址池格式有误，应为 IPv4 网段，例如 10.213.250.0/24。",
  ipv4: "预期出口格式有误，应为 IPv4 地址，例如 198.51.100.20。",
  server: "请填写服务器地址。",
  port: "请填写 1–65535 之间的端口。",
} as const;

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
