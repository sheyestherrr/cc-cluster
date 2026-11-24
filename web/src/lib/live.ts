// 定时取数的小钩子：页面可见时每隔一段时间取一次，切到后台就停。
import { useEffect, useRef, useState } from "react";

/**
 * 每隔 ms 毫秒调用一次 load，返回最近一次的结果（还没取到时是 null）。
 * deps 变了会立刻重新取；取数失败时保留上一次的结果。
 */
export function usePolling<T>(load: () => Promise<T>, ms: number, deps: readonly unknown[] = []): T | null {
  const [data, setData] = useState<T | null>(null);
  const fn = useRef(load);
  fn.current = load;
  useEffect(() => {
    let stale = false;
    const tick = () => {
      if (document.visibilityState !== "visible") return;
      fn.current().then(
        (v) => !stale && setData(v),
        () => {},
      );
    };
    tick();
    const timer = setInterval(tick, ms);
    document.addEventListener("visibilitychange", tick);
    return () => {
      stale = true;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ms, ...deps]);
  return data;
}
