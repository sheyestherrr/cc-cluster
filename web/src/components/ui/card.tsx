// 卡片与徽标：取自 Resin（MIT）webui 的 components/ui/Card.tsx 与 Badge.tsx。
import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <section className={cn("card", className)} {...props} />;
}

export type BadgeVariant = "neutral" | "success" | "warning" | "danger" | "info" | "accent" | "muted";

export function Badge({ className, variant = "neutral", ...props }: HTMLAttributes<HTMLSpanElement> & { variant?: BadgeVariant }) {
  return <span className={cn("badge", `badge-${variant}`, className)} {...props} />;
}
