// 按钮：样式类取自 Resin（MIT）webui 的 components/ui/Button.tsx，变体的名字沿用本项目原有的。
import * as React from "react";
import { cn } from "@/lib/utils";

const variants = {
  /** 主要操作。 */
  default: "btn-primary",
  destructive: "btn-danger",
  /** 次要操作，白底描边。 */
  outline: "btn-secondary",
  ghost: "btn-ghost",
  success: "btn-success",
} as const;

const sizes = {
  default: "btn-md",
  sm: "btn-sm",
  icon: "btn-sm btn-icon",
} as const;

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "default", size = "default", type = "button", ...props }, ref) => (
    <button ref={ref} type={type} className={cn("btn [&_svg]:h-4 [&_svg]:w-4 [&_svg]:shrink-0", variants[variant], sizes[size], className)} {...props} />
  ),
);
Button.displayName = "Button";
