// 定时取数的小钩子：页面可见时每隔一段时间取一次，切到后台就停。
import { useEffect, useRef, useState } from "react";
import { errorText } from "./api";

export interface Polled<T> {
  /** 最近一次的结果；尚未取到，或条件（deps）变化后新结果未返回时为 null。 */
  data: T | null;
  /** 最近一次读取失败的原因；成功后清空。 */
  error: string;
}

/**
 * 每隔 ms 毫秒调用一次 load。deps 变化时立即重新读取，并清掉按旧条件取到的结果，
 * 避免旧数据显示在新条件的标题下；条件不变时读取失败则保留上一次的结果，同时给出原因。
 */
export function usePolled<T>(load: () => Promise<T>, ms: number, deps: readonly unknown[] = []): Polled<T> {
  const [state, setState] = useState<Polled<T>>({ data: null, error: "" });
  const fn = useRef(load);
  fn.current = load;
  useEffect(() => {
    let stale = false;
    setState({ data: null, error: "" });
    const tick = () => {
      if (document.visibilityState !== "visible") return;
      fn.current().then(
        (data) => !stale && setState({ data, error: "" }),
        (e) => !stale && setState((s) => ({ data: s.data, error: errorText(e) })),
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
  return state;
}

/** 只要结果、不关心失败原因时用这个（例如每几秒刷新一次的实时速率）。 */
export function usePolling<T>(load: () => Promise<T>, ms: number, deps: readonly unknown[] = []): T | null {
  return usePolled(load, ms, deps).data;
}
