import type React from "react";
import { cn } from "@/lib/utils";

// 国旗图形取自 country-flag-icons（MIT）的 3x2 SVG。这里拿到的只是各个文件的地址，
// 图片在显示到的时候才请求，不进脚本。
const files = import.meta.glob<string>("../../node_modules/country-flag-icons/3x2/*.svg", { eager: true, query: "?url", import: "default" });
const urls: Record<string, string> = {};
for (const [path, url] of Object.entries(files)) {
  const code = path.slice(path.lastIndexOf("/") + 1, -".svg".length);
  urls[code] = url;
}

const names = typeof Intl.DisplayNames === "function" ? new Intl.DisplayNames(["zh-CN"], { type: "region" }) : null;

/** 国家或地区的中文名；认不出代码时原样返回。 */
export function regionName(code: string): string {
  try {
    return names?.of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

/** 地区是怎么得来的，写在提示里：从名称认出来的只是节点提供方的说法。 */
export function regionTitle(code: string, from?: string): string {
  const name = regionName(code);
  if (from === "exit") return `${name}（出口评估的所在地）`;
  if (from === "name") return `${name}（根据名称识别，不代表实际出口）`;
  return name;
}

export function hasFlag(code?: string): code is string {
  return !!code && code.toUpperCase() in urls;
}

/** 一面国旗；没有这个代码的图形时什么都不画。 */
export function Flag({ code, title, className }: { code?: string; title?: string; className?: string }) {
  if (!hasFlag(code)) return null;
  return (
    <img
      src={urls[code.toUpperCase()]}
      alt={code.toUpperCase()}
      title={title ?? regionName(code)}
      draggable={false}
      className={cn("aspect-[3/2] h-[13px] shrink-0 rounded-[2.5px] object-cover shadow-[0_0_0_0.5px_rgba(0,0,0,0.18)]", className)}
    />
  );
}

type Icon = React.ComponentType<{ className?: string }>;

/** 行首的方形图标位：知道地区时放国旗，否则放原来的图标。 */
export function RegionIcon({ code, from, icon: Fallback, size = "row" }: { code?: string; from?: string; icon: Icon; size?: "row" | "detail" }) {
  const detail = size === "detail";
  return (
    <span className={cn("tile-icon", detail && "h-11 w-11 basis-11")}>
      {hasFlag(code) ? (
        <Flag code={code} title={regionTitle(code, from)} className={detail ? "h-[20px]" : "h-[17px]"} />
      ) : (
        <Fallback className={detail ? "h-5 w-5" : "h-[18px] w-[18px]"} />
      )}
    </span>
  );
}
