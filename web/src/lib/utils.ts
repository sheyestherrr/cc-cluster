// 改编自 Claude-Hydra（MIT）app/src/lib/utils.ts，v1.1.6：时间显示改为接受 ISO 时间，另加了几个格式化函数。
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export const PALETTE = [
  "rgb(66 133 180)",
  "rgb(76 150 96)",
  "rgb(150 96 180)",
  "rgb(190 80 110)",
  "rgb(60 150 150)",
  "rgb(90 110 200)",
  "rgb(200 150 50)",
  "rgb(80 86 100)",
];

export function badgeLetter(name: string): string {
  const c = Array.from(name).find((ch) => /[\p{L}\p{N}]/u.test(ch)) ?? name.charAt(0);
  return (c ?? "?").toUpperCase();
}

/** 由名字得到一个稳定的调色板下标。 */
export function colorIndex(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h % PALETTE.length;
}

export function relativeTime(iso?: string | null): string {
  if (!iso) return "从未";
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return "从未";
  const min = Math.round((Date.now() - ms) / 60_000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const h = Math.round(min / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.round(h / 24);
  if (d < 30) return `${d} 天前`;
  return new Date(ms).toLocaleDateString("zh-CN");
}

export function clockTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("zh-CN", { hour12: false });
}

export function formatBytes(n?: number): string {
  if (!n) return "";
  const gib = n / 2 ** 30;
  return gib >= 1 ? `${+gib.toFixed(1)} GiB` : `${Math.round(n / 2 ** 20)} MiB`;
}
