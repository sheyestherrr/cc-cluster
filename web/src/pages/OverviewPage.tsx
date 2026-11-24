// 总览：几个要紧的数、流量的趋势、各槽位从哪里出去、持续检测的时间线、需要留意的事、订阅节点的延迟分布、最近的操作。
// “指标卡 + 趋势图 + 分布图 + 最近动态”的安排参考了 Resin（MIT）的看板页；代码是按本项目的组件重新写的，图用 recharts 画。
import { ArrowDown, ArrowUp, Box } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useState } from "react";
import type { PageId } from "@/components/AppShell";
import { DelayBars, Legend, SubUsableChart, TrafficChart, WatchTimeline, subLegend, trafficLegend, watchLegend } from "@/components/charts";
import { RegionIcon } from "@/components/Flag";
import { NoteText } from "@/components/NoteDialog";
import { ErrorBox, PageHeader, Tag } from "@/components/PageHeader";
import { Select } from "@/components/ui/input";
import { api, errorText, type Delay, type History, type Op, type Slot, type SlotLive, type State } from "@/lib/api";
import { usePolling } from "@/lib/live";
import { bytesText, cn, rateText, relativeTime } from "@/lib/utils";
import { stateText } from "@/pages/SlotsPage";

interface Props {
  state: State | null;
  loadError: string | null;
  go: (page: PageId) => void;
}

interface Notice {
  key: string;
  tone: "danger" | "warn";
  text: string;
  page: PageId;
}

/** 趋势往回看多久（小时）。 */
const RANGES: [number, string][] = [[24, "24 小时"], [168, "7 天"], [720, "30 天"]];

/** 需要留意的事：出口变了、连不上、被断网的槽位，没就绪的网关，有不利标记的落地，刷新失败的订阅。 */
function notices(state: State): Notice[] {
  const out: Notice[] = [];
  for (const s of state.slots) {
    if (s.watch?.status === "changed") out.push({ key: `slot:${s.id}:changed`, tone: "danger", text: `槽位 ${s.id} 的出口变了，已自动断网：${s.watch.detail ?? ""}`, page: "slots" });
    else if (s.watch?.status === "unreachable") out.push({ key: `slot:${s.id}:unreachable`, tone: "warn", text: `槽位 ${s.id} 自${relativeTime(s.watch.since)}起连不上出口`, page: "slots" });
    if (s.state === "hold") out.push({ key: `slot:${s.id}:hold`, tone: "warn", text: `槽位 ${s.id} 处于手动断网${s.hold_reason ? `：${s.hold_reason}` : ""}`, page: "slots" });
    else if (s.state === "offline") out.push({ key: `slot:${s.id}:offline`, tone: "warn", text: `槽位 ${s.id} 在运行，但没有接线（没有网络）`, page: "slots" });
    else if (s.state === "pending") out.push({ key: `slot:${s.id}:pending`, tone: "warn", text: `槽位 ${s.id} 的配置改过，还没应用`, page: "slots" });
  }
  for (const g of state.gateways) {
    if (g.container === "running" && !g.core_ready) out.push({ key: `gw:${g.id}`, tone: "warn", text: `网关 ${g.id} 在运行，但核心没有就绪`, page: "network" });
  }
  for (const n of state.nodes) {
    const flags = n.exit_report?.flags ?? [];
    if (n.role === "landing" && flags.length) out.push({ key: `node:${n.id}`, tone: "warn", text: `落地 ${n.id} 的出口有 ${flags.length} 项不利标记：${flags.map((f) => f.name).join("、")}`, page: "network" });
  }
  for (const s of state.subs) {
    if (s.last_error) out.push({ key: `sub:${s.id}`, tone: "warn", text: `订阅 ${s.id} ${relativeTime(s.checked_at)}刷新失败：${s.last_error}`, page: "subs" });
  }
  return out.sort((a, b) => Number(b.tone === "danger") - Number(a.tone === "danger"));
}

const card = { boxShadow: "var(--card-edge)" } as const;

export function OverviewPage({ state, loadError, go }: Props) {
  const [ops, setOps] = useState<Op[] | null>(null);
  const [delays, setDelays] = useState<Record<string, Delay[]> | null>(null);
  const [delayError, setDelayError] = useState("");
  const [hours, setHours] = useState(24);
  const traffic = usePolling(api.traffic, 5000);
  const history = usePolling(() => api.history(hours), 60_000, [hours]);

  useEffect(() => {
    api.ops().then(setOps, () => setOps([]));
  }, []);

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

  if (!state) {
    return (
      <div className="view-in w-full max-w-[1180px] pb-8">
        <PageHeader title="总览" subtitle="集群现在的样子。" />
        <div className="px-5">{loadError ? <ErrorBox text={loadError} /> : <div className="py-10 text-center text-[12.5px] text-muted">读取中…</div>}</div>
      </div>
    );
  }

  const wired = state.slots.filter((s) => s.state === "ready" || s.state === "pending").length;
  const running = state.gateways.filter((g) => g.container === "running").length;
  const landings = state.nodes.filter((n) => n.role === "landing");
  const flagged = landings.filter((n) => n.exit_report?.flags.length).length;
  const reported = landings.filter((n) => n.exit_report).length;
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

  const rangeText = RANGES.find(([h]) => h === hours)?.[1] ?? "";
  const totalDown = history?.traffic.reduce((sum, p) => sum + p.down, 0) ?? 0;
  const totalUp = history?.traffic.reduce((sum, p) => sum + p.up, 0) ?? 0;
  const watchSlots = state.slots.map((s) => s.id).filter((id) => history?.watch[id]?.length);

  return (
    <div className="view-in w-full max-w-[1180px] pb-8">
      <PageHeader
        title="总览"
        subtitle="集群现在的样子：槽位从哪里出去，走了多少流量，节点好不好用，最近做过什么。"
        actions={
          <div className="flex rounded-lg bg-pill p-0.5" role="group" aria-label="趋势的时间范围">
            {RANGES.map(([h, label]) => (
              <button
                key={h}
                type="button"
                aria-pressed={hours === h}
                onClick={() => setHours(h)}
                className={cn("rounded-md px-2.5 py-1 text-[12px] active:!scale-100", hours === h ? "bg-card font-medium text-fg shadow-[var(--edge)]" : "text-muted hover:text-fg")}
              >
                {label}
              </button>
            ))}
          </div>
        }
      />

      <div className="space-y-5 px-5">
        {loadError && <ErrorBox text={loadError} />}
        {state.docker_error && <ErrorBox text={`连不上 Docker，槽位和网关的状态读不到：${state.docker_error}`} />}

        <div className="grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(158px,1fr))]">
          <Tile label="槽位已接线" value={wired} of={state.slots.length} onClick={() => go("slots")}>
            {state.slots.length ? (wired === state.slots.length ? "都有网络" : `${state.slots.length - wired} 个没有网络`) : "还没有槽位"}
          </Tile>
          <Tile label="此刻下行" value={sampled ? rateText(downNow) : "—"} onClick={() => go("slots")}>
            {sampled ? <><ArrowUp className="h-3 w-3" aria-label="上行" />{rateText(upNow)} · {connsNow} 条连接</> : "等待第一次采样"}
          </Tile>
          <Tile label="网关运行中" value={running} of={state.gateways.length} onClick={() => go("network")}>
            {state.gateways.length ? `${state.nodes.filter((n) => n.role === "transit").length} 个中转节点` : "还没有网关"}
          </Tile>
          <Tile label="落地出口" value={landings.length} onClick={() => go("network")}>
            {!landings.length ? "还没有落地" : flagged ? <Tag tone="warn">{flagged} 个有不利标记</Tag> : reported ? "评估过的都没有不利标记" : "还没做出口评估"}
          </Tile>
          <Tile label="订阅节点" value={subNodes} onClick={() => go("subs")}>
            {!state.subs.length ? "还没有订阅" : subTested ? `${state.subs.length} 个订阅，测通 ${subUsable} 个` : `${state.subs.length} 个订阅，还没测过延迟`}
          </Tile>
          <Tile label="持续检测异常" value={abnormal} onClick={() => go("slots")}>
            {!watched.length ? "还没有检测结果" : abnormal ? <Tag tone="danger">出口变了或连不上</Tag> : <><Tag tone="ok">正常</Tag>{watched.length} 个槽位在检测</>}
          </Tile>
        </div>

        <section>
          <div className="group-label">流量</div>
          <div className="rounded-xl bg-card px-4 pb-2 pt-3.5" style={card}>
            <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-2 pb-3">
              <div className="flex items-end gap-6">
                <Total icon={<ArrowDown className="h-3 w-3" />} label={`${rangeText}下行`} value={bytesText(totalDown)} />
                <Total icon={<ArrowUp className="h-3 w-3" />} label={`${rangeText}上行`} value={bytesText(totalUp)} />
              </div>
              <Legend items={trafficLegend} />
            </div>
            {history === null ? (
              <Blank height={210}>读取中…</Blank>
            ) : totalDown + totalUp > 0 ? (
              <TrafficChart points={history.traffic} step={history.step} height={210} />
            ) : (
              <Blank height={210}>
                这段时间里还没有流量的记录。流量每五分钟记一档，在这个网页或 cc-cluster watch 开着的时候记录{traffic && !traffic.recording && sampled ? "；现在有另一个进程在记录，这里稍后就能看到" : ""}。
              </Blank>
            )}
          </div>
        </section>

        <div className="grid items-start gap-5 min-[900px]:grid-cols-[minmax(0,6fr)_minmax(0,5fr)]">
          <div className="space-y-5">
            <section>
              <div className="group-label">槽位的出口</div>
              {state.slots.length ? (
                <div className="list-card">
                  {state.slots.map((s) => <SlotLine key={s.id} s={s} state={state} live={traffic?.slots[s.id]} onClick={() => go("slots")} />)}
                </div>
              ) : (
                <Hint action="去添加" onClick={() => go("slots")}>还没有槽位。槽位是一个隔离的工作环境，流量只从它自己的落地出去。</Hint>
              )}
            </section>

            {!!state.slots.length && (
              <section>
                <div className="flex items-end justify-between gap-3">
                  <div className="group-label">持续检测</div>
                  <div className="pb-1.5"><Legend items={watchLegend} /></div>
                </div>
                <div className="rounded-xl bg-card px-3 py-2.5" style={card}>
                  {history === null ? (
                    <Blank height={64}>读取中…</Blank>
                  ) : watchSlots.length ? (
                    <WatchTimeline slots={watchSlots} watch={history.watch} from={history.from} to={history.to} />
                  ) : (
                    <Blank height={64}>这段时间里没有检测记录。持续检测每隔几分钟看一遍已接线槽位的连通性与出口，结果画在这里。</Blank>
                  )}
                </div>
              </section>
            )}

            <section>
              <div className="flex items-baseline justify-between">
                <div className="group-label">最近的操作</div>
                <button type="button" onClick={() => go("system")} className="px-1 pb-1.5 text-[11.5px] text-muted hover:text-fg">全部记录</button>
              </div>
              <div className="list-card">
                {ops === null && <div className="px-4 py-3 text-[12.5px] text-muted">读取中…</div>}
                {ops?.length === 0 && <div className="px-4 py-3 text-[12.5px] text-muted">还没有操作记录。</div>}
                {ops?.slice(0, 8).map((op, i) => (
                  <div key={`${op.time}-${i}`} className="flex items-start gap-3 px-4 py-2 text-[12px]">
                    <span className={cn("mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full", op.ok ? "bg-ok" : "bg-danger")} role="img" aria-label={op.ok ? "成功" : "失败"} />
                    <span className="w-[68px] shrink-0 text-[11.5px] text-muted">{relativeTime(op.time)}</span>
                    <span className="w-28 shrink-0 truncate font-mono text-[11.5px] text-fg-2">{op.op}</span>
                    <span className="selectable min-w-0 flex-1 break-words text-muted">
                      {op.target && <span className="mr-1.5 text-fg-2">{op.target}</span>}
                      {op.ok ? op.detail : op.error}
                    </span>
                  </div>
                ))}
              </div>
            </section>
          </div>

          <div className="space-y-5">
            <section>
              <div className="group-label">需要留意</div>
              <div className="list-card">
                {!attention.length && <div className="flex items-center gap-2 px-4 py-3 text-[12.5px] text-muted"><Tag tone="ok">正常</Tag>没有需要处理的事</div>}
                {attention.map((n) => (
                  <button key={n.key} type="button" onClick={() => go(n.page)} className="flex w-full items-start gap-2.5 px-4 py-2.5 text-left hover:bg-card-2 active:!scale-100">
                    <Tag tone={n.tone}>{n.tone === "danger" ? "异常" : "留意"}</Tag>
                    <span className="min-w-0 flex-1 break-words text-[12.5px] leading-[18px] text-fg-2">{n.text}</span>
                  </button>
                ))}
              </div>
            </section>

            <SubsSection state={state} delays={delays} error={delayError} history={history} rangeText={rangeText} goSubs={() => go("subs")} />
          </div>
        </div>
      </div>
    </div>
  );
}

function Tile({ label, value, of, onClick, children }: { label: string; value: React.ReactNode; of?: number; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} style={card} className="flex min-w-0 flex-col items-start gap-0.5 rounded-xl bg-card px-4 py-3 text-left hover:bg-card-2 active:!scale-100">
      <span className="text-[11.5px] text-muted">{label}</span>
      <span className="max-w-full truncate text-[24px] font-semibold leading-8 tracking-[-0.02em]">
        {value}
        {of !== undefined && <span className="ml-1 text-[13px] font-normal tracking-normal text-muted">/ {of}</span>}
      </span>
      <span className="flex min-h-[18px] flex-wrap items-center gap-1.5 text-[11.5px] leading-[18px] text-muted">{children}</span>
    </button>
  );
}

function Total({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div>
      <div className="flex items-center gap-1 text-[11.5px] text-muted">{icon}{label}</div>
      <div className="text-[20px] font-semibold leading-7 tracking-[-0.02em]">{value}</div>
    </div>
  );
}

function Blank({ height, children }: { height: number; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-center px-6 text-center text-[12px] leading-5 text-muted" style={{ minHeight: height }}>
      <span className="max-w-[460px]">{children}</span>
    </div>
  );
}

function Hint({ children, action, onClick }: { children: React.ReactNode; action: string; onClick: () => void }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-dashed border-line px-4 py-4 text-[12.5px] leading-5 text-muted">
      <span className="min-w-0 flex-1">{children}</span>
      <button type="button" onClick={onClick} className="shrink-0 font-medium text-fg-2 hover:text-fg">{action}</button>
    </div>
  );
}

/** 一个槽位：行首是出口所在地的国旗，右边是此刻的速率和实际的出口地址。 */
function SlotLine({ s, state, live, onClick }: { s: Slot; state: State; live?: SlotLive; onClick: () => void }) {
  const node = state.nodes.find((n) => n.id === s.landing);
  return (
    <button type="button" onClick={onClick} className="flex w-full items-center gap-3 px-4 py-2 text-left hover:bg-card-2 active:!scale-100">
      <RegionIcon code={node?.region} from={node?.region_from} icon={Box} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate text-[13px] font-medium">{s.id}</span>
          <NoteText note={s.note} />
          {s.watch?.status === "changed" && <Tag tone="danger" title={s.watch.detail}>出口变了</Tag>}
          {s.watch?.status === "unreachable" && <Tag tone="warn" title={s.watch.detail}>连不上</Tag>}
          {s.state === "hold" && <Tag tone="warn" title={s.hold_reason}>已断网</Tag>}
        </div>
        <div className="truncate text-[11.5px] text-muted">
          {stateText[s.state]} · 落地 {s.landing}{node?.direct ? "（直出）" : ` · 网关 ${s.gateway}`}
        </div>
      </div>
      {live && (
        <div className="hidden shrink-0 text-right font-mono text-[11px] leading-[17px] text-muted min-[560px]:block" style={{ fontVariantNumeric: "tabular-nums" }} title={`${live.conns} 条连接`}>
          <div className="flex items-center justify-end gap-0.5"><ArrowDown className="h-3 w-3" aria-label="下行" />{rateText(live.down_bps)}</div>
          <div className="flex items-center justify-end gap-0.5"><ArrowUp className="h-3 w-3" aria-label="上行" />{rateText(live.up_bps)}</div>
        </div>
      )}
      <div className="w-[128px] shrink-0 text-right">
        <div className="selectable truncate font-mono text-[11.5px] text-fg-2">{s.exit_ip || "—"}</div>
        {s.exit_where && <div className="truncate text-[11px] text-muted">{s.exit_where}</div>}
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

function SubsSection({ state, delays, error, history, rangeText, goSubs }: { state: State; delays: Record<string, Delay[]> | null; error: string; history: History | null; rangeText: string; goSubs: () => void }) {
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
  const trendSub = chosen || (state.subs.length === 1 ? state.subs[0].id : "");
  const trend = trendSub ? (history?.subs[trendSub] ?? []) : [];

  return (
    <section>
      <div className="flex items-baseline justify-between gap-3">
        <div className="group-label">订阅节点的延迟</div>
        {state.subs.length > 1 && (
          <Select aria-label="订阅" value={chosen} onChange={(e) => setSub(e.target.value)} className="mb-1.5 h-6 w-auto text-[11.5px]">
            <option value="">全部订阅</option>
            {state.subs.map((s) => <option key={s.id} value={s.id}>{s.id}</option>)}
          </Select>
        )}
      </div>
      {!state.subs.length ? (
        <Hint action="去添加" onClick={goSubs}>还没有订阅。添加之后，这里显示它的节点延迟落在哪些区间。</Hint>
      ) : (
        <div className="rounded-xl bg-card px-3 pb-3 pt-3" style={card}>
          {error && <ErrorBox text={error} />}
          {!error && delays === null && <Blank height={120}>读取中…</Blank>}
          {!error && delays !== null && !ok.length && (
            <div className="flex items-center gap-3 px-1 py-6 text-[12.5px] leading-5 text-muted">
              <span className="min-w-0 flex-1">
                {nodes.length ? `${nodes.length} 个节点里${dead ? `有 ${dead} 个不通，其余` : ""}还没测过延迟。` : "订阅里没有节点。"}
              </span>
              <button type="button" onClick={goSubs} className="shrink-0 font-medium text-fg-2 hover:text-fg">去订阅页测</button>
            </div>
          )}
          {!error && !!ok.length && (
            <>
              <p className="px-1 pb-1 text-[11.5px] leading-[18px] text-muted">
                <span className="font-medium text-fg">测通 {ok.length} 个</span>，中位数 {median} 毫秒{dead ? `；不通 ${dead} 个` : ""}{untested ? `；没测 ${untested} 个` : ""}
              </p>
              <DelayBars buckets={buckets} total={ok.length} />
            </>
          )}

          {!error && delays !== null && (
            <div className="mt-3 border-t-[0.5px] border-line-2 pt-3">
              <div className="flex flex-wrap items-center justify-between gap-2 px-1 pb-1">
                <span className="text-[11.5px] text-muted">{trendSub ? `${trendSub} 每次测延迟后测通的个数（${rangeText}）` : "测通个数的变化"}</span>
                {trend.length > 0 && <Legend items={subLegend} />}
              </div>
              {!trendSub ? (
                <Blank height={56}>在右上角选一个订阅，看它测通的个数随时间的变化。</Blank>
              ) : trend.length && history ? (
                <SubUsableChart points={trend} from={history.from} to={history.to} />
              ) : (
                <Blank height={56}>这段时间里没有测过。每在订阅页测一次延迟，这里多一个点。</Blank>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
