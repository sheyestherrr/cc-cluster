// 应用的入口：定时读取集群的现状，按当前页面渲染。
import { useCallback, useEffect, useState } from "react";
import { AppShell, pageTitles, type PageId } from "@/components/AppShell";
import { api, errorText, type State } from "@/lib/api";
import { NetworkPage } from "@/pages/NetworkPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { SlotsPage } from "@/pages/SlotsPage";
import { SubsPage } from "@/pages/SubsPage";
import { SystemPage } from "@/pages/SystemPage";

/** 页面开着的时候每隔这么久重新读取一次现状：容器可能被命令行或别的操作改动。 */
const REFRESH_MS = 8000;
const APP_NAME = "cc-cluster";

export default function App() {
  const [page, setPage] = useState<PageId>("overview");
  /** 槽位页里打开着详情的那个槽位。 */
  const [slot, setSlot] = useState<string | null>(null);
  const [state, setState] = useState<State | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setState(await api.state());
      setLoadError(null);
    } catch (e) {
      setLoadError(errorText(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(() => document.visibilityState === "visible" && refresh(), REFRESH_MS);
    const onFocus = () => refresh();
    window.addEventListener("focus", onFocus);
    return () => {
      clearInterval(timer);
      window.removeEventListener("focus", onFocus);
    };
  }, [refresh]);

  // 标签页标题：当前页面在前，开着几个集群的页面时靠集群名区分。
  const cluster = state?.cluster.name;
  useEffect(() => {
    document.title = [pageTitles[page], cluster !== APP_NAME && cluster, APP_NAME].filter(Boolean).join(" · ");
  }, [page, cluster]);

  // 换页时回到页面顶部；从侧栏点当前这一页，也从详情页回到卡片网格（节点与订阅页靠重新挂载）。
  const [visit, setVisit] = useState(0);
  const go = useCallback((next: PageId, target?: string) => {
    setPage(next);
    setVisit((n) => n + 1);
    if (next === "slots") setSlot(target ?? null);
    window.scrollTo({ top: 0 });
  }, []);

  const ready = state?.slots.filter((s) => s.state === "ready" || s.state === "pending").length ?? 0;

  return (
    <AppShell activePage={page} onPageChange={go} cluster={cluster} status={state ? { ready, total: state.slots.length } : null}>
      {page === "overview" && <OverviewPage state={state} loadError={loadError} go={go} />}
      {page === "slots" && <SlotsPage state={state} loadError={loadError} refresh={refresh} goNetwork={() => go("network")} open={slot} setOpen={setSlot} />}
      {page === "network" && <NetworkPage key={visit} state={state} loadError={loadError} refresh={refresh} goSubs={() => go("subs")} />}
      {page === "subs" && <SubsPage key={visit} state={state} loadError={loadError} refresh={refresh} />}
      {page === "system" && <SystemPage state={state} refresh={refresh} />}
    </AppShell>
  );
}
