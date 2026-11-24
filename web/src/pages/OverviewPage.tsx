// 总览：几个要紧的数、各槽位从哪里出去、需要留意的事、订阅节点的延迟分布、最近的操作。
// “指标卡 + 分布图 + 最近动态”的安排参考了 Resin（MIT）的看板页；代码是按本项目的组件重新写的，
// 数据只来自现有的 state、ops 和订阅节点接口，图是手写的 SVG。
import { Box } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { PageId } from "@/components/AppShell";
import { RegionIcon } from "@/components/Flag";
import { NoteText } from "@/components/NoteDialog";
import { ErrorBox, PageHeader, Tag } from "@/components/PageHeader";
import { Select } from "@/components/ui/input";
import { api, errorText, type Delay, type Op, type Slot, type State } from "@/lib/api";
import { cn, relativeTime } from "@/lib/utils";
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

export function OverviewPage({ state, loadError, go }: Props) {
  const [ops, setOps] = useState<Op[] | null>(null);
  const [delays, setDelays] = useState<Record<string, Delay[]> | null>(null);
  const [delayError, setDelayError] = useState("");

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
      <div className="view-in w-full max-w-[1100px] pb-8">
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

  return (
    <div className="view-in w-full max-w-[1100px] pb-8">
      <PageHeader title="总览" subtitle="集群现在的样子：槽位从哪里出去，节点好不好用，最近做过什么。" />

      <div className="space-y-5 px-5">
        {loadError && <ErrorBox text={loadError} />}
        {state.docker_error && <ErrorBox text={`连不上 Docker，槽位和网关的状态读不到：${state.docker_error}`} />}

        <div className="grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(150px,1fr))]">
          <Tile label="槽位已接线" value={wired} of={state.slots.length} onClick={() => go("slots")}>
            {state.slots.length ? (wired === state.slots.length ? "都有网络" : `${state.slots.length - wired} 个没有网络`) : "还没有槽位"}
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

        <div className="grid items-start gap-5 min-[900px]:grid-cols-[minmax(0,6fr)_minmax(0,5fr)]">
          <div className="space-y-5">
            <section>
              <div className="group-label">槽位的出口</div>
              {state.slots.length ? (
                <div className="list-card">
                  {state.slots.map((s) => <SlotLine key={s.id} s={s} state={state} onClick={() => go("slots")} />)}
                </div>
              ) : (
                <Hint action="去添加" onClick={() => go("slots")}>还没有槽位。槽位是一个隔离的工作环境，流量只从它自己的落地出去。</Hint>
              )}
            </section>

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

            <DelaySection state={state} delays={delays} error={delayError} goSubs={() => go("subs")} />
          </div>
        </div>
      </div>
    </div>
  );
}

function Tile({ label, value, of, onClick, children }: { label: string; value: number; of?: number; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} style={{ boxShadow: "var(--card-edge)" }} className="flex min-w-0 flex-col items-start gap-0.5 rounded-xl bg-card px-4 py-3 text-left hover:bg-card-2 active:!scale-100">
      <span className="text-[11.5px] text-muted">{label}</span>
      <span className="text-[24px] font-semibold leading-8 tracking-[-0.02em]">
        {value}
        {of !== undefined && <span className="ml-1 text-[13px] font-normal tracking-normal text-muted">/ {of}</span>}
      </span>
      <span className="flex min-h-[18px] flex-wrap items-center gap-1.5 text-[11.5px] leading-[18px] text-muted">{children}</span>
    </button>
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

/** 一个槽位：行首是出口所在地的国旗，右边是实际的出口地址。 */
function SlotLine({ s, state, onClick }: { s: Slot; state: State; onClick: () => void }) {
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
      <div className="shrink-0 text-right">
        <div className="selectable font-mono text-[11.5px] text-fg-2">{s.exit_ip || "—"}</div>
        {s.exit_where && <div className="max-w-[180px] truncate text-[11px] text-muted">{s.exit_where}</div>}
      </div>
    </button>
  );
}

// ---- 延迟分布 ----

/** 延迟的分档（毫秒）：below 是这一档的上界（不含）。 */
const BUCKETS = [
  { label: "<100", below: 100 },
  { label: "100–200", below: 200 },
  { label: "200–300", below: 300 },
  { label: "300–500", below: 500 },
  { label: "500–1000", below: 1000 },
  { label: "≥1000", below: Infinity },
];

function DelaySection({ state, delays, error, goSubs }: { state: State; delays: Record<string, Delay[]> | null; error: string; goSubs: () => void }) {
  const [sub, setSub] = useState("");
  const chosen = state.subs.some((s) => s.id === sub) ? sub : "";
  // 提示项（剩余流量、到期时间之类）不是线路，不算在内。
  const nodes = useMemo(
    () => Object.entries(delays ?? {}).filter(([id]) => !chosen || id === chosen).flatMap(([, list]) => list).filter((d) => !d.info),
    [delays, chosen],
  );
  const ok = nodes.filter((d) => d.delay_ms > 0).map((d) => d.delay_ms).sort((a, b) => a - b);
  const dead = nodes.filter((d) => d.delay_ms < 0).length;
  const untested = nodes.filter((d) => d.delay_ms === 0).length;

  return (
    <section>
      <div className="flex items-baseline justify-between gap-3">
        <div className="group-label">订阅节点的延迟分布</div>
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
        <div className="rounded-xl bg-card px-4 py-3" style={{ boxShadow: "var(--card-edge)" }}>
          {error && <ErrorBox text={error} />}
          {!error && delays === null && <div className="py-8 text-center text-[12.5px] text-muted">读取中…</div>}
          {!error && delays !== null && !ok.length && (
            <div className="flex items-center gap-3 py-6 text-[12.5px] leading-5 text-muted">
              <span className="min-w-0 flex-1">
                {nodes.length ? `${nodes.length} 个节点里${dead ? `有 ${dead} 个不通，其余` : ""}还没测过延迟。` : "订阅里没有节点。"}
              </span>
              <button type="button" onClick={goSubs} className="shrink-0 font-medium text-fg-2 hover:text-fg">去订阅页测</button>
            </div>
          )}
          {!error && !!ok.length && <DelayChart delays={ok} dead={dead} untested={untested} />}
        </div>
      )}
    </section>
  );
}

/** 纵轴的上限：不小于 n 的整齐的数，保证一半也是整数。 */
function niceTop(n: number): number {
  const v = Math.max(n, 2);
  const pow = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 4, 6, 8, 10]) {
    if (m * pow >= v && (m * pow) % 2 === 0) return m * pow;
  }
  return 10 * pow;
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => setWidth(Math.round(entry.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

/** 柱状图：测通的节点按延迟分档各有多少个。delays 是测通的延迟（毫秒），从小到大。 */
function DelayChart({ delays, dead, untested }: { delays: number[]; dead: number; untested: number }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [active, setActive] = useState<number | null>(null);
  const counts = BUCKETS.map((b, i) => delays.filter((d) => d < b.below && d >= (i ? BUCKETS[i - 1].below : 0)).length);
  const top = niceTop(Math.max(...counts));
  const median = delays[Math.floor((delays.length - 1) / 2)];

  const H = 172;
  const pad = { left: 30, right: 4, top: 22, bottom: 22 };
  const plotW = Math.max(width - pad.left - pad.right, 0);
  const plotH = H - pad.top - pad.bottom;
  const base = pad.top + plotH;
  const band = plotW / BUCKETS.length;
  const barW = Math.max(Math.min(24, band - 8), 2);
  const y = (n: number) => base - (n / top) * plotH;

  return (
    <div ref={ref}>
      {width > 0 && (
        <svg width={width} height={H} role="group" aria-label="订阅节点的延迟分布，单位毫秒" className="block select-none">
          {[0, top / 2, top].map((t) => (
            <g key={t}>
              <line x1={pad.left} x2={width - pad.right} y1={y(t)} y2={y(t)} className="stroke-line-2" strokeWidth={1} shapeRendering="crispEdges" />
              <text x={pad.left - 6} y={y(t) + 3.5} textAnchor="end" className="fill-muted text-[10.5px]" style={{ fontVariantNumeric: "tabular-nums" }}>{t}</text>
            </g>
          ))}
          {BUCKETS.map((b, i) => {
            const n = counts[i];
            const cx = pad.left + band * (i + 0.5);
            const x = cx - barW / 2;
            const yTop = y(n);
            const r = Math.min(4, base - yTop, barW / 2);
            return (
              <g key={b.label}>
                {n > 0 && (
                  <path
                    d={`M${x},${base} V${yTop + r} Q${x},${yTop} ${x + r},${yTop} H${x + barW - r} Q${x + barW},${yTop} ${x + barW},${yTop + r} V${base} Z`}
                    className={cn("fill-[#4f46e5] dark:fill-[#6366f1]", active !== null && active !== i && "opacity-45")}
                  />
                )}
                {active === i && <text x={cx} y={yTop - 6} textAnchor="middle" className="fill-fg text-[11.5px] font-semibold">{n}</text>}
                <text x={cx} y={H - 6} textAnchor="middle" className={cn("text-[10.5px]", active === i ? "fill-fg" : "fill-muted")}>{b.label}</text>
                <rect
                  x={pad.left + band * i}
                  y={0}
                  width={band}
                  height={H}
                  fill="transparent"
                  tabIndex={0}
                  role="img"
                  aria-label={`${b.label} 毫秒：${n} 个`}
                  className="outline-hidden"
                  onPointerEnter={() => setActive(i)}
                  onPointerLeave={() => setActive((a) => (a === i ? null : a))}
                  onFocus={() => setActive(i)}
                  onBlur={() => setActive((a) => (a === i ? null : a))}
                />
              </g>
            );
          })}
        </svg>
      )}
      <p className="mt-1 min-h-[18px] text-[11.5px] leading-[18px] text-muted">
        {active !== null ? (
          <><span className="font-medium text-fg">{counts[active]} 个</span>在 {BUCKETS[active].label} 毫秒，占测通的 {Math.round((counts[active] / delays.length) * 100)}%</>
        ) : (
          <><span className="font-medium text-fg">测通 {delays.length} 个</span>，中位数 {median} 毫秒{dead ? `；不通 ${dead} 个` : ""}{untested ? `；没测 ${untested} 个` : ""}</>
        )}
      </p>
      <table className="sr-only">
        <caption>测通的订阅节点按延迟分档</caption>
        <thead><tr><th>延迟（毫秒）</th><th>节点数</th></tr></thead>
        <tbody>{BUCKETS.map((b, i) => <tr key={b.label}><td>{b.label}</td><td>{counts[i]}</td></tr>)}</tbody>
      </table>
    </div>
  );
}
