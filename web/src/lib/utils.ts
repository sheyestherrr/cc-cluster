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

/** 只要日期：2026/11/27。 */
export function dateText(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString("zh-CN");
}

/** 字节数写成带单位的短形式：812 B、3.4 KB、120 MB。 */
export function bytesText(n?: number): string {
  const v = n ?? 0;
  if (v < 1024) return `${Math.round(v)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let x = v / 1024;
  let i = 0;
  while (x >= 1024 && i < units.length - 1) {
    x /= 1024;
    i++;
  }
  return `${x >= 100 ? Math.round(x) : +x.toFixed(1)} ${units[i]}`;
}

/** 速率（字节每秒）。 */
export function rateText(bps?: number): string {
  return `${bytesText(bps)}/s`;
}

/** 一段时间的短写法：8 秒、5 分钟、2.5 小时。 */
export function spanText(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.round(s / 60)} 分钟`;
  if (s < 86400) return `${+(s / 3600).toFixed(1)} 小时`;
  return `${+(s / 86400).toFixed(1)} 天`;
}

/**
 * 镜像引用去掉仓库前缀，只留“名称:标签”：ghcr.io/owner/app:v1 → app:v1。
 * 按摘要固定的引用只保留摘要的前 12 位。
 */
export function imageName(ref?: string): string {
  const full = (ref ?? "").trim();
  const cut = full.lastIndexOf("/", full.includes("@") ? full.indexOf("@") : full.length);
  return full.slice(cut + 1).replace(/@sha256:([0-9a-f]{12})[0-9a-f]+$/, "@sha256:$1…");
}

export function formatBytes(n?: number): string {
  if (!n) return "";
  const gib = n / 2 ** 30;
  return gib >= 1 ? `${+gib.toFixed(1)} GiB` : `${Math.round(n / 2 ** 20)} MiB`;
}
