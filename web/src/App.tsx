// 应用的入口：定时读取集群的现状，按当前页面渲染。
import { useCallback, useEffect, useRef, useState } from "react";
import { ActionsProvider } from "@/components/Actions";
import { AppShell, pageTitles } from "@/components/AppShell";
import { UpdateDialog } from "@/components/UpdateDialog";
import { api, errorText, isUnauthorized, type State, type UpdateInfo } from "@/lib/api";
import { useRoute } from "@/lib/route";
import { isWired } from "@/lib/slot-state";
import { NetworkPage } from "@/pages/NetworkPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { SlotsPage } from "@/pages/SlotsPage";
import { SubsPage } from "@/pages/SubsPage";
import { SystemPage } from "@/pages/SystemPage";

/** 页面开着的时候每隔这么久重新读取一次现状：容器可能被命令行或别的操作改动。 */
const REFRESH_MS = 8000;
/** 每隔这么久问一次有没有新版本。cc-cluster 自己记着上次查到的结果，隔几个小时才真的去问 GitHub。 */
const UPDATE_POLL_MS = 30 * 60_000;
const APP_NAME = "cc-cluster";

export default function App() {
  // 当前页面与打开的对象取自地址栏：刷新后回到原处，前进后退可用。
  const [route, go] = useRoute();
  const page = route.page;
  const [state, setState] = useState<State | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [locked, setLocked] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setState(await api.state());
      setLoadError(null);
      setLocked(false);
    } catch (e) {
      setLoadError(errorText(e));
      setLocked(isUnauthorized(e));
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

  // 标签页标题：打开的对象在前，其次是页面；同时打开多个集群的页面时，靠容器名前缀区分。
  const cluster = state?.cluster.name;
  const target = route.path[route.path.length - 1];
  useEffect(() => {
    document.title = [target, pageTitles[page], cluster, APP_NAME].filter(Boolean).join(" · ");
  }, [target, page, cluster]);

  // 各页打开着详情的对象；null 时显示列表。
  const one = route.path[0] ?? null;
  const networkOpen = (route.path[0] === "gateway" || route.path[0] === "node") && route.path[1] ? { kind: route.path[0], id: route.path[1] } as const : null;

  // 版本与更新：读到现状（令牌没问题）之后问一次，之后隔一阵再问。查不成不打扰，弹窗里才显示原因。
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [updateOpen, setUpdateOpen] = useState(false);
  const [checking, setChecking] = useState(false);
  const [updateError, setUpdateError] = useState("");
  const checkUpdate = useCallback(async (manual = false) => {
    if (manual) setChecking(true);
    try {
      setUpdate(await api.update(manual));
      setUpdateError("");
    } catch (e) {
      setUpdateError(errorText(e));
    } finally {
      if (manual) setChecking(false);
    }
  }, []);
  const loaded = state !== null;
  useEffect(() => {
    if (!loaded) return;
    void checkUpdate();
    const timer = setInterval(() => document.visibilityState === "visible" && void checkUpdate(), UPDATE_POLL_MS);
    return () => clearInterval(timer);
  }, [loaded, checkUpdate]);

  // 页面打开之后 cc-cluster 被更新过：接口已经是新版本的，这个页面还是旧的，提示刷新。
  const version = state?.version;
  const firstVersion = useRef<string | undefined>(undefined);
  if (version && !firstVersion.current) firstVersion.current = version;
  const stale = !!version && version !== firstVersion.current;

  const ready = state?.slots.filter(isWired).length ?? 0;

  // 没有令牌（第一次用这个浏览器，或令牌换过）：只显示怎么拿到链接，不显示一堆读取失败的页面。
  if (locked) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <div className="card max-w-[520px] p-7">
          <h1 className="text-[20px]">需要访问链接</h1>
          <p className="mt-3 text-[14px] leading-7 text-fg-2">请在运行 cc-cluster 的机器上执行：</p>
          <pre className="mono mt-2 rounded-xl border border-line bg-white/70 px-3.5 py-2.5 text-[14px]">cc-cluster web</pre>
          <p className="mt-3 text-[14px] leading-7 text-fg-2">然后在浏览器中打开输出的带 <span className="mono">#token=</span> 的地址。此后该浏览器可直接访问，无需再带令牌。</p>
        </div>
      </div>
    );
  }

  return (
    <ActionsProvider refresh={refresh}>
    <AppShell
      activePage={page}
      version={version}
      newVersion={update?.newer ? update.latest?.version : undefined}
      stale={stale}
      onVersionClick={() => setUpdateOpen(true)}
      status={state ? { ready, total: state.slots.length } : null}
    >
      {page === "overview" && <OverviewPage state={state} loadError={loadError} go={go} />}
      {page === "slots" && <SlotsPage state={state} loadError={loadError} refresh={refresh} goNetwork={() => go("network")} goSubs={() => go("subs")} open={one} setOpen={(id) => (id ? go("slots", id) : go("slots"))} />}
      {page === "network" && <NetworkPage state={state} loadError={loadError} refresh={refresh} goSubs={() => go("subs")} open={networkOpen} setOpen={(o) => (o ? go("network", o.kind, o.id) : go("network"))} />}
      {page === "subs" && <SubsPage state={state} loadError={loadError} refresh={refresh} open={one} setOpen={(id) => (id ? go("subs", id) : go("subs"))} />}
      {page === "system" && <SystemPage state={state} loadError={loadError} refresh={refresh} />}
      <UpdateDialog open={updateOpen} onClose={() => setUpdateOpen(false)} version={version} info={update} checking={checking} error={updateError} onCheck={() => void checkUpdate(true)} />
    </AppShell>
    </ActionsProvider>
  );
}
