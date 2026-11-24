// 结构取自 Claude-Hydra（MIT）app/src/App.tsx，v1.1.6；数据来源换成了 cc-cluster 的 HTTP 接口。
import { useCallback, useEffect, useState } from "react";
import { AppShell, pageTitles, type PageId } from "@/components/AppShell";
import { api, errorText, type State } from "@/lib/api";
import { useTheme } from "@/lib/theme";
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
  const [theme, setTheme] = useTheme();
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

  const ready = state?.slots.filter((s) => s.state === "ready" || s.state === "pending").length ?? 0;

  return (
    <AppShell activePage={page} onPageChange={setPage} theme={theme} onThemeChange={setTheme}
      status={state ? { ready, total: state.slots.length } : null}>
      {page === "overview" && <OverviewPage state={state} loadError={loadError} go={setPage} />}
      {page === "slots" && <SlotsPage state={state} loadError={loadError} refresh={refresh} goNetwork={() => setPage("network")} />}
      {page === "network" && <NetworkPage state={state} loadError={loadError} refresh={refresh} goSubs={() => setPage("subs")} />}
      {page === "subs" && <SubsPage state={state} loadError={loadError} refresh={refresh} />}
      {page === "system" && <SystemPage state={state} refresh={refresh} theme={theme} onThemeChange={setTheme} />}
    </AppShell>
  );
}
