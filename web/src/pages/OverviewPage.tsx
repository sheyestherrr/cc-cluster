// 总览看板：四个指标卡、流量与连接的趋势、各槽位从哪里出去、需要留意的事、持续检测的时间线、
// 订阅节点的延迟分布与测通个数、最近的操作。
// 版面（页头 + 指标卡 + 三列的面板网格）和样式取自 Resin（MIT）webui 的 features/dashboard/DashboardPage.tsx，内容是本项目的。
import { Activity, Box, Gauge, Layers, ShieldCheck, Sparkles, Waves } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useState } from "react";
import type { PageId } from "@/components/AppShell";
import { ChartEmpty, ChartPending, ConnsChart, DelayBars, Legend, SubUsableChart, TrafficChart, WatchTimeline, subLegend, trafficLegend, watchLegend } from "@/components/charts";
import { RegionIcon } from "@/components/Flag";
import { ErrorBox, Page, PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { Select } from "@/components/ui/input";
import { api, errorText, type Delay, type History, type Slot, type SlotLive, type State } from "@/lib/api";
import { usePolled, usePolling } from "@/lib/live";
import { opDetail, opName } from "@/lib/ops";
import { bytesText, rateText, relativeTime } from "@/lib/utils";
import { stateText } from "@/pages/SlotsPage";

interface Props {
  state: State | null;
  loadError: string | null;
  go: (page: PageId, target?: string) => void;
}

interface Notice {
  key: string;
  tone: "danger" | "warning";
  text: string;
  page: PageId;
  target?: string;
}

/** 趋势往回看多久（小时）。 */
const RANGES: [number, string][] = [[24, "最近 24 小时"], [168, "最近 7 天"], [720, "最近 30 天"]];

/** 需要留意的事：出口变了、连不上、被断网的槽位，没就绪的网关，有不利标记的落地，刷新失败的订阅。 */
function notices(state: State): Notice[] {
  const out: Notice[] = [];
  for (const s of state.slots) {
    const at = { page: "slots" as const, target: s.id };
    if (s.watch?.status === "changed") out.push({ key: `slot:${s.id}:changed`, tone: "danger", text: `槽位 ${s.id} 的出口已变更，已自动断网：${s.watch.detail ?? ""}`, ...at });
    else if (s.watch?.status === "unreachable") out.push({ key: `slot:${s.id}:unreachable`, tone: "warning", text: `槽位 ${s.id} 自${relativeTime(s.watch.since)}起无法连接出口`, ...at });
    if (s.state === "hold") out.push({ key: `slot:${s.id}:hold`, tone: "warning", text: `槽位 ${s.id} 已手动断网${s.hold_reason ? `：${s.hold_reason}` : ""}`, ...at });
    else if (s.state === "offline") out.push({ key: `slot:${s.id}:offline`, tone: "warning", text: `槽位 ${s.id} 正在运行，但未接线（无网络）`, ...at });
    else if (s.state === "pending") out.push({ key: `slot:${s.id}:pending`, tone: "warning", text: `槽位 ${s.id} 的配置已修改，尚未应用`, ...at });
  }
  for (const g of state.gateways) {
    if (g.container === "running" && !g.core_ready) out.push({ key: `gw:${g.id}`, tone: "warning", text: `网关 ${g.id} 正在运行，但核心未就绪`, page: "network" });
  }
  for (const n of state.nodes) {
    const flags = n.exit_report?.flags ?? [];
    if (n.role === "landing" && flags.length) out.push({ key: `node:${n.id}`, tone: "warning", text: `落地 ${n.id} 的出口有 ${flags.length} 项不利标记：${flags.map((f) => f.name).join("、")}`, page: "network" });
  }
  for (const s of state.subs) {
    if (s.last_error) out.push({ key: `sub:${s.id}`, tone: "warning", text: `订阅 ${s.id} ${relativeTime(s.checked_at)}刷新失败：${s.last_error}`, page: "subs" });
  }
  return out.sort((a, b) => Number(b.tone === "danger") - Number(a.tone === "danger"));
}

export function OverviewPage({ state, loadError, go }: Props) {
  const [delays, setDelays] = useState<Record<string, Delay[]> | null>(null);
  const [delayError, setDelayError] = useState("");
  const [hours, setHours] = useState(24);
  const traffic = usePolling(api.traffic, 5000);
  const { data: history, error: historyError } = usePolled(() => api.history(hours), 60_000, [hours]);
  /** 历史数据尚未取到时占位的文字：读取失败与加载中要分得清。 */
  const historyPending = <ChartPending error={historyError} what="历史数据" />;
  // 操作记录随页面定时更新：在别的页面做过的操作，回到总览时能看到。
  const { data: ops, error: opsError } = usePolled(api.ops, 30_000);

  // 订阅刷新过或测过延迟之后，重新读各订阅的节点。
  const loaded = !!state;
  const subsKey = (state?.subs ?? []).map((s) => `${s.id}@${s.fetched_at}@${s.tested_at ?? ""}`).join("|");
  useEffect(() => {
    if (!loaded) return;
    let stale = false;
    const ids = subsKey ? subsKey.split("|").map((part) => part.split("@")[0]) : [];
    Promise.all(ids.map(async (id) => [id, await api.subNodes(id)] as const)).then(
      (all) => {
        if (stale) return;
        setDelays(Object.fromEntries(all));
        setDelayError("");
      },
      (e) => !stale && setDelayError(errorText(e)),
    );
    return () => {
      stale = true;
    };
  }, [loaded, subsKey]);

  const attention = useMemo(() => (state ? notices(state) : []), [state]);

  const header = (
    <PageHeader
      title="总览看板"
      subtitle="槽位、流量与出口一览。"
      actions={
        <div className="dashboard-header-controls">
          <label className="dashboard-control">
            <span>时间范围</span>
            <Select value={hours} onChange={(e) => setHours(Number(e.target.value))}>
              {RANGES.map(([h, label]) => <option key={h} value={h}>{label}</option>)}
            </Select>
          </label>
        </div>
      }
    />
  );

  if (!state) {
    return (
      <Page className="dashboard-page">
        {header}
        {loadError ? <ErrorBox text={loadError} /> : <Card className="dashboard-panel"><p className="muted">正在加载集群状态…</p></Card>}
      </Page>
    );
  }

  const wired = state.slots.filter((s) => s.state === "ready" || s.state === "pending").length;
  const running = state.gateways.filter((g) => g.container === "running").length;
  const landings = state.nodes.filter((n) => n.role === "landing");
  const transits = state.nodes.filter((n) => n.role === "transit").length;
  const flagged = landings.filter((n) => n.exit_report?.flags.length).length;
  const subNodes = state.subs.reduce((sum, s) => sum + s.count, 0);
  const subUsable = state.subs.reduce((sum, s) => sum + (s.tested_at ? s.usable : 0), 0);
  const subTested = state.subs.some((s) => s.tested_at);
  const watched = state.slots.filter((s) => s.watch);
  const abnormal = watched.filter((s) => s.watch!.status !== "ok").length;

  const live = Object.values(traffic?.slots ?? {});
  const sampled = !!traffic && Date.parse(traffic.at) > 0;
  const downNow = live.reduce((sum, s) => sum + s.down_bps, 0);
  const upNow = live.reduce((sum, s) => sum + s.up_bps, 0);
  const connsNow = live.reduce((sum, s) => sum + s.conns, 0);

  const rangeText = (RANGES.find(([h]) => h === hours)?.[1] ?? "").replace("最近 ", "");
  const totalDown = history?.traffic.reduce((sum, p) => sum + p.down, 0) ?? 0;
  const totalUp = history?.traffic.reduce((sum, p) => sum + p.up, 0) ?? 0;
  const peakConns = history?.traffic.reduce((m, p) => Math.max(m, p.conns ?? 0), 0) ?? 0;
  const watchSlots = state.slots.map((s) => s.id).filter((id) => history?.watch[id]?.length);
  const noHistory = `该时段内暂无记录。流量每 5 分钟记录一次，仅在网页界面或 cc-cluster watch 运行期间记录${traffic && !traffic.recording && sampled ? "；当前由另一进程记录，稍后可在此查看" : ""}。`;

  return (
    <Page className="dashboard-page">
      {header}
      {loadError && <ErrorBox text={loadError} />}
      {state.docker_error && <ErrorBox text={`无法连接 Docker，无法读取槽位与网关的状态：${state.docker_error}`} />}

      <div className="dashboard-kpi-grid">
        <Kpi icon={<Waves size={20} />} tone="waves" label="实时吞吐" value={sampled ? rateText(downNow + upNow) : "—"}>
          {sampled ? `下行 ${rateText(downNow)} · 上行 ${rateText(upNow)}` : "等待首次采样"}
        </Kpi>
        <Kpi icon={<Gauge size={20} />} tone="gauge" label="实时连接数" value={sampled ? connsNow : "—"}>
          {sampled ? `${live.filter((s) => s.conns > 0).length} 个槽位有连接 · ${rangeText}峰值 ${peakConns}` : "等待首次采样"}
        </Kpi>
        <Kpi
          icon={<ShieldCheck size={20} />}
          tone={abnormal ? "bad" : "shield"}
          label="槽位已接线"
          value={<>{wired}<small className="ml-1.5 text-[14px] font-semibold text-muted">/ {state.slots.length}</small></>}
          badge={!watched.length ? undefined : abnormal ? <Badge variant="danger">{abnormal} 个异常</Badge> : <Badge variant="success">检测正常</Badge>}
        >
          {!state.slots.length ? "暂无槽位" : wired === state.slots.length ? "全部有网络" : `${state.slots.length - wired} 个无网络`}
          {` · 网关 ${running} / ${state.gateways.length} 运行`}
        </Kpi>
        <Kpi
          icon={<Layers size={20} />}
          tone={flagged ? "warn" : "lease"}
          label="落地出口"
          value={landings.length}
          badge={flagged ? <Badge variant="warning">{flagged} 个有标记</Badge> : undefined}
        >
          {`中转 ${transits} 个 · `}
          {!state.subs.length ? "暂无订阅" : subTested ? `订阅节点可用 ${subUsable} / ${subNodes}` : `订阅节点 ${subNodes} 个`}
        </Kpi>
      </div>

      <div className="dashboard-main-grid">
        <Card className="dashboard-panel span-2">
          <PanelHead title="流量趋势" hint={`各槽位合计的下行 / 上行（${history ? (history.step >= 3600 ? `每 ${history.step / 3600} 小时一个数据点` : `每 ${history.step / 60} 分钟一个数据点`) : historyError ? "读取失败" : "加载中…"}）`}>
            <div className="panel-totals">
              <div className="panel-total">
                <span><i style={{ background: "var(--chart-1)" }} />{rangeText}下行</span>
                <strong>{bytesText(totalDown)}</strong>
              </div>
              <div className="panel-total">
                <span><i style={{ background: "var(--chart-2)" }} />{rangeText}上行</span>
                <strong>{bytesText(totalUp)}</strong>
              </div>
            </div>
          </PanelHead>
          {history === null ? <div style={{ minHeight: 260 }} className="grid">{historyPending}</div> : totalDown + totalUp > 0 ? <TrafficChart points={history.traffic} step={history.step} /> : <ChartEmpty height={260}>{noHistory}</ChartEmpty>}
          <Legend items={trafficLegend} />
        </Card>

        <Card className="dashboard-panel">
          <PanelHead title="连接峰值" hint="同时连接数的最大值" />
          {history === null ? <div style={{ minHeight: 260 }} className="grid">{historyPending}</div> : peakConns > 0 ? <ConnsChart points={history.traffic} step={history.step} /> : <ChartEmpty height={260}>该时段内无连接记录。</ChartEmpty>}
          <Legend items={[{ color: "var(--chart-3)", label: "连接数" }]} />
        </Card>

        <Card className="dashboard-panel span-2">
          <PanelHead title="槽位的出口" hint="各槽位的落地、速率与出口地址">
            <Button variant="outline" size="sm" onClick={() => go("slots")}>全部槽位</Button>
          </PanelHead>
          {state.slots.length ? (
            <div className="dashboard-snapshot-list">
              {state.slots.map((s) => <SlotLine key={s.id} s={s} state={state} live={traffic?.slots[s.id]} onClick={() => go("slots", s.id)} />)}
            </div>
          ) : (
            <div className="empty-box plain">
              <Sparkles size={16} />
              <p>暂无槽位。槽位是隔离的工作环境，流量只经其落地出口发出。</p>
              <Button size="sm" onClick={() => go("slots")}>添加槽位</Button>
            </div>
          )}
        </Card>

        <Card className="dashboard-panel">
          <PanelHead title="需要关注" hint="异常与待处理事项" />
          {!attention.length ? (
            <div className="callout callout-success"><ShieldCheck size={15} /><span>暂无待处理事项。</span></div>
          ) : (
            <div className="dashboard-snapshot-list">
              {attention.map((n) => (
                <button key={n.key} type="button" className="snapshot-row items-start" onClick={() => go(n.page, n.target)}>
                  <Badge variant={n.tone}>{n.tone === "danger" ? "异常" : "注意"}</Badge>
                  <span className="min-w-0 flex-1 break-words leading-5">{n.text}</span>
                </button>
              ))}
            </div>
          )}
        </Card>

        {!!state.slots.length && (
          <Card className="dashboard-panel span-2">
            <PanelHead title="持续检测" hint="已接线槽位的连通性与出口">
              <Legend items={watchLegend} />
            </PanelHead>
            {history === null ? (
              <div style={{ minHeight: 120 }} className="grid">{historyPending}</div>
            ) : watchSlots.length ? (
              <WatchTimeline slots={watchSlots} watch={history.watch} from={history.from} to={history.to} />
            ) : (
              <ChartEmpty height={120}>该时段内无检测记录。持续检测仅在网页界面或 cc-cluster watch 运行期间进行。</ChartEmpty>
            )}
          </Card>
        )}

        <Card className={`dashboard-panel${state.slots.length ? "" : " span-2"}`}>
          <PanelHead title="最近操作" hint="最近 8 条">
            <Button variant="outline" size="sm" onClick={() => go("system")}>全部记录</Button>
          </PanelHead>
          {ops === null && (opsError ? <ChartPending error={opsError} what="操作记录" /> : <p className="muted">加载中…</p>)}
          {ops?.length === 0 && <div className="empty-box plain"><p>暂无操作记录。</p></div>}
          {!!ops?.length && (
            <div className="dashboard-snapshot-list">
              {ops.slice(0, 8).map((op, i) => (
                <div key={`${op.time}-${i}`} className="snapshot-row">
                  <span className={`status-dot ${op.ok ? "ok" : "bad"}`} role="img" aria-label={op.ok ? "成功" : "失败"} />
                  <div className="grow">
                    <p title={op.op}>{opName(op.op)}{op.target && <span className="mono ml-1.5 font-normal text-fg-2">{op.target}</span>}</p>
                    {(op.ok ? op.detail : op.error) && <p title={op.ok ? op.detail : op.error}>{op.ok ? opDetail(op.detail) : op.error}</p>}
                  </div>
                  <span className="side">{relativeTime(op.time)}</span>
                </div>
              ))}
            </div>
          )}
        </Card>

        <SubsPanels state={state} delays={delays} error={delayError} history={history} rangeText={rangeText} goSubs={() => go("subs")} />
      </div>
    </Page>
  );
}

function Kpi({ icon, tone, label, value, badge, children }: { icon: React.ReactNode; tone: string; label: string; value: React.ReactNode; badge?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Card className="dashboard-kpi-card">
      <div className={`dashboard-kpi-icon ${tone}`}>{icon}</div>
      <div className="min-w-0">
        <p className="dashboard-kpi-label">{label}</p>
        <p className="dashboard-kpi-value num truncate">{value}</p>
        <p className="dashboard-kpi-sub">{children}</p>
      </div>
      {badge && <div className="dashboard-kpi-badge">{badge}</div>}
    </Card>
  );
}

function PanelHead({ title, hint, children }: { title: string; hint?: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
      <div className="dashboard-panel-header">
        <h3>{title}</h3>
        {hint && <p>{hint}</p>}
      </div>
      {children}
    </div>
  );
}

/** 一个槽位：行首是出口所在地的国旗，右边是此刻的速率和实际的出口地址。 */
function SlotLine({ s, state, live, onClick }: { s: Slot; state: State; live?: SlotLive; onClick: () => void }) {
  const node = state.nodes.find((n) => n.id === s.landing);
  const wired = s.state === "ready" || s.state === "pending";
  return (
    <button type="button" className="snapshot-row" onClick={onClick}>
      <RegionIcon code={node?.region} from={node?.region_from} icon={Box} />
      <div className="grow">
        <p className="flex items-center gap-2">
          <span className="truncate">{s.id}</span>
          {s.note && <span className="truncate text-[12px] font-normal text-muted">{s.note}</span>}
          {s.watch?.status === "changed" && <Badge variant="danger" title={s.watch.detail}>出口已变更</Badge>}
          {s.watch?.status === "unreachable" && <Badge variant="warning" title={s.watch.detail}>无法连接</Badge>}
          {s.state === "hold" && <Badge variant="warning" title={s.hold_reason}>已断网</Badge>}
        </p>
        <p>
          <span className={`status-dot mr-1.5 inline-block align-middle ${wired ? "ok" : s.state === "hold" || s.state === "offline" ? "warn" : ""}`} />
          {stateText[s.state]} · 落地 {s.landing}{node?.direct ? "（直出）" : ` · 网关 ${s.gateway}`}
        </p>
      </div>
      {live && (
        <div className="side num hidden min-[720px]:block" title={`${live.conns} 条连接`}>
          <div className="flex items-center justify-end gap-1.5"><Activity size={12} style={{ color: "var(--chart-1)" }} />↓ {rateText(live.down_bps)}</div>
          <div>↑ {rateText(live.up_bps)}</div>
        </div>
      )}
      <div className="side w-[150px]">
        <div className="mono truncate text-[13px] font-semibold text-fg">{s.exit_ip || "—"}</div>
        {s.exit_where && <div className="truncate">{s.exit_where}</div>}
      </div>
    </button>
  );
}

// ---- 订阅节点：延迟分布与测通个数的变化 ----

/** 延迟的分档（毫秒）：below 是这一档的上界（不含）。 */
const BUCKETS = [
  { label: "<100", below: 100 },
  { label: "100–200", below: 200 },
  { label: "200–300", below: 300 },
  { label: "300–500", below: 500 },
  { label: "500–1000", below: 1000 },
  { label: "≥1000", below: Infinity },
];

function SubsPanels({ state, delays, error, history, rangeText, goSubs }: { state: State; delays: Record<string, Delay[]> | null; error: string; history: History | null; rangeText: string; goSubs: () => void }) {
  const [sub, setSub] = useState("");
  const chosen = state.subs.some((s) => s.id === sub) ? sub : "";
  // 提示项（剩余流量、到期时间之类）不是线路，不算在内。
  const nodes = useMemo(
    () => Object.entries(delays ?? {}).filter(([id]) => !chosen || id === chosen).flatMap(([, list]) => list).filter((d) => !d.info),
    [delays, chosen],
  );
  const ok = useMemo(() => nodes.filter((d) => d.delay_ms > 0).map((d) => d.delay_ms).sort((a, b) => a - b), [nodes]);
  const dead = nodes.filter((d) => d.delay_ms < 0).length;
  const untested = nodes.filter((d) => d.delay_ms === 0).length;
  const buckets = useMemo(() => BUCKETS.map((b, i) => ({ label: b.label, count: ok.filter((d) => d < b.below && d >= (i ? BUCKETS[i - 1].below : 0)).length })), [ok]);
  const median = ok[Math.floor((ok.length - 1) / 2)];
  // 测通个数的变化按订阅看：只有一个订阅时就是它。
  const trendSub = chosen || (state.subs.length === 1 ? state.subs[0].id : state.subs[0]?.id ?? "");
  const trend = trendSub ? (history?.subs[trendSub] ?? []) : [];

  if (!state.subs.length) {
    return (
      <Card className="dashboard-panel span-2">
        <PanelHead title="订阅节点" hint="延迟分布与可用节点数的变化" />
        <div className="empty-box plain">
          <Sparkles size={16} />
          <p>暂无订阅。添加后，此处显示节点的延迟分布。</p>
          <Button size="sm" onClick={goSubs}>添加订阅</Button>
        </div>
      </Card>
    );
  }

  return (
    <>
      <Card className="dashboard-panel">
        <PanelHead title="节点延迟分布" hint={chosen ? `订阅 ${chosen} 最近一次延迟测试的结果` : "全部订阅最近一次延迟测试的结果"}>
          {state.subs.length > 1 && (
            <Select aria-label="订阅" value={chosen} onChange={(e) => setSub(e.target.value)} className="field-sm w-auto">
              <option value="">全部订阅</option>
              {state.subs.map((s) => <option key={s.id} value={s.id}>{s.id}</option>)}
            </Select>
          )}
        </PanelHead>
        {error && <ErrorBox text={error} />}
        {!error && delays === null && <ChartEmpty height={226}>加载中…</ChartEmpty>}
        {!error && delays !== null && !ok.length && (
          <div className="empty-box plain" style={{ minHeight: 226 }}>
            <p>{nodes.length ? `${nodes.length} 个节点中${dead ? `有 ${dead} 个不可用，其余` : ""}尚未测试延迟。` : "订阅中没有节点。"}</p>
            <Button size="sm" variant="outline" onClick={goSubs}>前往“订阅管理”页测试</Button>
          </div>
        )}
        {!error && !!ok.length && (
          <>
            <DelayBars buckets={buckets} total={ok.length} />
            <div className="dashboard-summary-inline">
              <span>可用 <b>{ok.length}</b> 个</span>
              <span>中位数 <b>{median}</b> 毫秒</span>
              {dead > 0 && <span>不可用 <b>{dead}</b> 个</span>}
              {untested > 0 && <span>未测试 <b>{untested}</b> 个</span>}
            </div>
          </>
        )}
      </Card>

      <Card className={`dashboard-panel${state.slots.length ? " span-2" : ""}`}>
        <PanelHead title="可用节点数的变化" hint={trendSub ? `订阅 ${trendSub} 每次延迟测试后的可用节点数（${rangeText}）` : "请选择订阅"}>
          {trend.length > 0 && <Legend items={subLegend} />}
        </PanelHead>
        {trend.length && history ? (
          <SubUsableChart points={trend} from={history.from} to={history.to} height={226} />
        ) : (
          <ChartEmpty height={226}>该时段内未进行延迟测试。每次在“订阅管理”页测试延迟后，此处增加一个数据点。</ChartEmpty>
        )}
      </Card>
    </>
  );
}
