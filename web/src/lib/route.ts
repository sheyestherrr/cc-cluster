// 当前页面与打开的对象记在地址栏的 hash 里：刷新后回到原处，浏览器的前进后退可用，页面可以收藏。
//   #/slots            槽位列表
//   #/slots/work       槽位 work 的详情
//   #/network/node/hk  节点 hk 的详情
import { useCallback, useEffect, useState } from "react";

export const PAGES = ["overview", "slots", "network", "subs", "system"] as const;
export type PageId = (typeof PAGES)[number];

export interface Route {
  page: PageId;
  /** 页面之下的部分，例如 ["work"] 或 ["node", "hk"]；列表页为空。 */
  path: string[];
}

function decode(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

/** 无法识别的 hash（包括空的）一律是总览。 */
export function parseRoute(hash: string): Route {
  const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(decode);
  const page = PAGES.find((p) => p === parts[0]);
  return page ? { page, path: parts.slice(1) } : { page: "overview", path: [] };
}

export function routeHash(page: PageId, path: readonly string[] = []): string {
  return `#/${[page, ...path].map(encodeURIComponent).join("/")}`;
}

export type Navigate = (page: PageId, ...path: string[]) => void;

export function useRoute(): [Route, Navigate] {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));
  useEffect(() => {
    const sync = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, []);
  // 主动跳转时回到页面顶部；前进后退时的滚动位置由浏览器恢复。
  const go = useCallback<Navigate>((page, ...path) => {
    const hash = routeHash(page, path);
    if (window.location.hash !== hash) window.location.hash = hash;
    window.scrollTo({ top: 0 });
  }, []);
  return [route, go];
}
