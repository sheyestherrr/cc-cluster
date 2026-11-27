// 图表：都用 recharts 画。趋势图、柱状图的样子（带框的画布、虚线网格、平滑曲线、提示框）取自 Resin（MIT）
// webui 的 features/dashboard/DashboardPage.tsx；持续检测的时间线是本项目自己的。
// 配色与 Resin 的看板一致：下行蓝、上行绿，变量在 styles/app.css。
import { AlertTriangle } from "lucide-react";
import type React from "react";
import { useId, useMemo } from "react";
import { Area, Bar, BarChart, CartesianGrid, ComposedChart, Line, Rectangle, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { HistoryPoint, SubPoint, WatchSpan, WatchStatus } from "@/lib/api";
import { bytesText, cn, spanText } from "@/lib/utils";

const C1 = "var(--chart-1)";
const C2 = "var(--chart-2)";
const C3 = "var(--chart-3)";
const GRID = "var(--chart-grid)";
const TICK = { fill: "var(--chart-tick)", fontSize: 11, fontWeight: 600 } as const;
const AXIS = { tickLine: false, axisLine: false, tick: TICK } as const;
const CURSOR = { stroke: "rgba(15, 94, 216, 0.34)", strokeWidth: 1 } as const;
const MARGIN = { top: 10, right: 12, bottom: 4, left: 8 } as const;
/** 图框内右上角有图例时的边距：顶部多留一行。 */
const MARGIN_LEGEND = { ...MARGIN, top: 34 } as const;

const pad = (n: number) => String(n).padStart(2, "0");
const hm = (ms: number) => {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
};
const md = (ms: number) => {
  const d = new Date(ms);
  return `${d.getMonth() + 1}/${d.getDate()}`;
};
const mdhm = (ms: number) => `${md(ms)} ${hm(ms)}`;

/** 时间轴上的刻度：跨度在两天以内按小时取整，更长的按天取整。 */
function timeTicks(from: number, to: number): { ticks: number[]; format: (ms: number) => string } {
  const span = to - from;
  const hour = 3600_000;
  const day = 24 * hour;
  if (span <= 2 * day) {
    const step = [1, 2, 3, 4, 6, 12].map((h) => h * hour).find((s) => span / s <= 8) ?? 12 * hour;
    const ticks: number[] = [];
    const first = new Date(from);
    first.setMinutes(0, 0, 0);
    for (let t = first.getTime(); t <= to; t += hour) {
      if (t >= from && new Date(t).getHours() % (step / hour) === 0) ticks.push(t);
    }
    return { ticks, format: hm };
  }
  const step = Math.max(1, Math.ceil(span / day / 8));
  const ticks: number[] = [];
  const first = new Date(from);
  first.setHours(0, 0, 0, 0);
  for (let t = first.getTime(), i = 0; t <= to; t += day, i++) {
    if (t >= from && i % step === 0) ticks.push(t);
  }
  return { ticks, format: md };
}

/** 字节数的纵轴：上限取不小于 max 的“整”值（1、2、4、8、10、20… 乘以 KB / MB / GB），分四格。 */
function byteTicks(max: number): number[] {
  let top = 1024;
  outer: for (let unit = 1024; ; unit *= 1024) {
    for (const m of [1, 2, 4, 8, 10, 20, 40, 80, 100, 200, 400, 800]) {
      top = m * unit;
      if (top >= max) break outer;
    }
  }
  return [0, top / 4, top / 2, (top * 3) / 4, top];
}

/** 数量（连接数）纵轴的刻度：上限是不小于最大值的整数，四等分后仍是整数。 */
export function countTicks(max: number): number[] {
  let top = 4;
  outer: for (let unit = 1; ; unit *= 10) {
    for (const m of [4, 8, 12, 20]) {
      top = m * unit;
      if (top >= max) break outer;
    }
  }
  return [0, top / 4, top / 2, (top * 3) / 4, top];
}

// ---- 画布、图例、提示框 ----

/** 带框的画布，下面一行是时间范围的两端。 */
/**
 * 图的外框。summary 是图的文字概述，只给读屏软件：图形本身读不出数值。
 * legend 是画在图框内右上角的图例；带图例的图用 MARGIN_LEGEND 留出顶部的位置，曲线不与图例重叠。
 */
function Plot({ height, footer, summary, legend, children, className }: { height?: number; footer?: [string, string]; summary: string; legend?: { color: string; label: string }[]; children: React.ReactNode; className?: string }) {
  return (
    <div className="trend-chart">
      <p className="sr-only">{summary}</p>
      <div className={cn("trend-svg", className)} style={height ? { height } : undefined}>
        {legend && <div className="chart-legend"><Legend items={legend} /></div>}
        {children}
      </div>
      {footer && (
        <div className="trend-footer">
          <span>{footer[0]}</span>
          <span>{footer[1]}</span>
        </div>
      )}
    </div>
  );
}

/** 没有数据时占住图的位置的一句话。warn 为真时带警告图标：只用于读取失败，暂无数据不是异常。 */
export function ChartEmpty({ height = 180, warn, children }: { height?: number; warn?: boolean; children: React.ReactNode }) {
  return (
    <div className="empty-box dashboard-empty" style={{ minHeight: height }}>
      {warn && <AlertTriangle size={14} className="shrink-0" />}
      <p className="max-w-[520px] text-[13px]">{children}</p>
    </div>
  );
}

/** 数据尚未取到时的占位：读取失败时说明原因，否则是“加载中…”。高度由外层决定。 */
export function ChartPending({ error, what }: { error: string; what: string }) {
  return <ChartEmpty height={0} warn={!!error}>{error ? `无法读取${what}：${error}` : "加载中…"}</ChartEmpty>;
}

export function Legend({ items }: { items: { color: string; label: string }[] }) {
  return (
    <div className="dashboard-legend">
      {items.map((it) => (
        <span key={it.label}>
          <i style={{ background: it.color }} />
          {it.label}
        </span>
      ))}
    </div>
  );
}

function Tip({ title, rows, note }: { title: React.ReactNode; rows: { color?: string; label: string; value: React.ReactNode }[]; note?: React.ReactNode }) {
  return (
    <div className="trend-tooltip">
      <p className="trend-tooltip-time">{title}</p>
      <div className="trend-tooltip-list">
        {rows.map((r) => (
          <p key={r.label} className="trend-tooltip-row">
            <span>
              {r.color && <i style={{ background: r.color }} aria-hidden="true" />}
              {r.label}
            </span>
            <b>{r.value}</b>
          </p>
        ))}
      </div>
      {note && <p className="mt-1.5 max-w-[220px] whitespace-normal">{note}</p>}
    </div>
  );
}

interface TipProps<Row> {
  active?: boolean;
  payload?: readonly { dataKey?: unknown; payload?: Row }[];
}

const trafficLegend = [
  { color: C1, label: "下行" },
  { color: C2, label: "上行" },
];
const connsLegend = [{ color: C3, label: "连接数" }];

// ---- 流量 ----

interface TrafficRow {
  t: number;
  down: number;
  up: number;
  conns: number;
}

/** 流量的趋势：每一档里的上下行字节，下行带渐变的面积。slot 给了就只画那个槽位的。 */
export function TrafficChart({ points, step, slot, height = 260 }: { points: HistoryPoint[]; step: number; slot?: string; height?: number }) {
  const rows = useMemo<TrafficRow[]>(
    () =>
      points.map((p) => {
        const v = slot ? p.slots?.[slot] : p;
        return { t: Date.parse(p.t), down: v?.down ?? 0, up: v?.up ?? 0, conns: v?.conns ?? 0 };
      }),
    [points, slot],
  );
  const id = useId().replace(/:/g, "");
  if (!rows.length) return null;
  const from = rows[0].t;
  const to = rows[rows.length - 1].t + step * 1000;
  const { ticks, format } = timeTicks(from, to);
  const long = to - from > 2 * 24 * 3600_000;
  const stamp = long ? mdhm : hm;
  const yTicks = byteTicks(rows.reduce((m, r) => Math.max(m, r.down, r.up), 0));
  const summary = `流量趋势，${mdhm(from)} 至 ${mdhm(to)}：下行合计 ${bytesText(rows.reduce((n, r) => n + r.down, 0))}，上行合计 ${bytesText(rows.reduce((n, r) => n + r.up, 0))}。`;

  return (
    <Plot height={height} footer={[mdhm(from), mdhm(to)]} summary={summary} legend={trafficLegend}>
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={rows} margin={MARGIN_LEGEND}>
          <defs>
            <linearGradient id={`${id}-down`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={C1} stopOpacity={0.26} />
              <stop offset="100%" stopColor={C1} stopOpacity={0.02} />
            </linearGradient>
            <linearGradient id={`${id}-up`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={C2} stopOpacity={0.18} />
              <stop offset="100%" stopColor={C2} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke={GRID} strokeDasharray="2 4" vertical={false} />
          <XAxis dataKey="t" type="number" scale="time" domain={[from, to]} ticks={ticks} tickFormatter={format} {...AXIS} tickMargin={6} height={24} />
          <YAxis width={64} tickMargin={4} domain={[0, yTicks[4]]} ticks={yTicks} tickFormatter={(v: number) => (v ? bytesText(v) : "0")} {...AXIS} />
          <Tooltip
            isAnimationActive={false}
            cursor={CURSOR}
            wrapperStyle={{ outline: "none" }}
            content={({ active, payload }: TipProps<TrafficRow>) => {
              const r = payload?.[0]?.payload;
              if (!active || !r) return null;
              return (
                <Tip
                  title={`${stamp(r.t)} – ${hm(r.t + step * 1000)}`}
                  rows={[
                    { color: C1, label: "下行", value: bytesText(r.down) },
                    { color: C2, label: "上行", value: bytesText(r.up) },
                    ...(r.conns > 0 ? [{ label: "连接峰值", value: `${r.conns} 条` }] : []),
                  ]}
                />
              );
            }}
          />
          <Area type="monotone" dataKey="down" stroke="none" fill={`url(#${id}-down)`} dot={false} activeDot={false} isAnimationActive={false} />
          <Area type="monotone" dataKey="up" stroke="none" fill={`url(#${id}-up)`} dot={false} activeDot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="down" stroke={C1} strokeWidth={2} dot={false} activeDot={{ r: 3.5, stroke: "#ffffff", strokeWidth: 1.5, fill: C1 }} isAnimationActive={false} />
          <Line type="monotone" dataKey="up" stroke={C2} strokeWidth={2} dot={false} activeDot={{ r: 3.5, stroke: "#ffffff", strokeWidth: 1.5, fill: C2 }} isAnimationActive={false} />
        </ComposedChart>
      </ResponsiveContainer>
    </Plot>
  );
}

/** 连接数的趋势：每一档里同时连接数的峰值。 */
export function ConnsChart({ points, step, slot, height = 260 }: { points: HistoryPoint[]; step: number; slot?: string; height?: number }) {
  const rows = useMemo(() => points.map((p) => ({ t: Date.parse(p.t), conns: (slot ? p.slots?.[slot]?.conns : p.conns) ?? 0 })), [points, slot]);
  const id = useId().replace(/:/g, "");
  if (!rows.length) return null;
  const from = rows[0].t;
  const to = rows[rows.length - 1].t + step * 1000;
  const { ticks, format } = timeTicks(from, to);
  const long = to - from > 2 * 24 * 3600_000;
  const peak = rows.reduce((m, r) => Math.max(m, r.conns), 0);
  const yTicks = countTicks(peak);
  return (
    <Plot height={height} footer={[mdhm(from), mdhm(to)]} summary={`连接数趋势，${mdhm(from)} 至 ${mdhm(to)}：峰值 ${peak} 条。`} legend={connsLegend}>
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={rows} margin={MARGIN_LEGEND}>
          <defs>
            <linearGradient id={`${id}-c`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={C3} stopOpacity={0.24} />
              <stop offset="100%" stopColor={C3} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke={GRID} strokeDasharray="2 4" vertical={false} />
          <XAxis dataKey="t" type="number" scale="time" domain={[from, to]} ticks={ticks.filter((_, i) => i % 2 === 0)} tickFormatter={format} {...AXIS} tickMargin={6} height={24} />
          <YAxis width={36} tickMargin={4} allowDecimals={false} domain={[0, yTicks[yTicks.length - 1]]} ticks={yTicks} {...AXIS} />
          <Tooltip
            isAnimationActive={false}
            cursor={CURSOR}
            wrapperStyle={{ outline: "none" }}
            content={({ active, payload }: TipProps<{ t: number; conns: number }>) => {
              const r = payload?.[0]?.payload;
              if (!active || !r) return null;
              return <Tip title={`${long ? mdhm(r.t) : hm(r.t)} – ${hm(r.t + step * 1000)}`} rows={[{ color: C3, label: "连接峰值", value: `${r.conns} 条` }]} />;
            }}
          />
          <Area type="monotone" dataKey="conns" stroke="none" fill={`url(#${id}-c)`} dot={false} activeDot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="conns" stroke={C3} strokeWidth={2} dot={false} activeDot={{ r: 3.5, stroke: "#ffffff", strokeWidth: 1.5, fill: C3 }} isAnimationActive={false} />
        </ComposedChart>
      </ResponsiveContainer>
    </Plot>
  );
}

// ---- 延迟分布 ----

export interface Bucket {
  label: string;
  count: number;
}

/** 柱状图：各档延迟里有多少个节点。 */
export function DelayBars({ buckets, total, height = 226 }: { buckets: Bucket[]; total: number; height?: number }) {
  const id = useId().replace(/:/g, "");
  return (
    <div className="histogram-chart" style={{ height }}>
      <p className="sr-only">延迟分布（毫秒）：{buckets.map((b) => `${b.label} ${b.count} 个`).join("，")}。</p>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={buckets} margin={{ top: 14, right: 12, bottom: 4, left: 4 }}>
          <defs>
            <linearGradient id={`${id}-bar`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#2388ff" stopOpacity={0.94} />
              <stop offset="100%" stopColor="#0f5ed8" stopOpacity={0.88} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke={GRID} strokeDasharray="2 4" vertical={false} />
          <XAxis dataKey="label" {...AXIS} tickMargin={6} interval={0} height={24} />
          <YAxis width={34} allowDecimals={false} tickCount={5} {...AXIS} />
          <Tooltip
            isAnimationActive={false}
            cursor={{ fill: "rgba(20, 112, 255, 0.08)" }}
            wrapperStyle={{ outline: "none" }}
            content={({ active, payload }: TipProps<Bucket>) => {
              const r = payload?.[0]?.payload;
              if (!active || !r) return null;
              return (
                <div className="histogram-tooltip">
                  <p className="histogram-tooltip-title">{r.label} 毫秒</p>
                  <p className="histogram-tooltip-value">
                    节点数 {r.count}
                    {total ? `，占可用节点的 ${Math.round((r.count / total) * 100)}%` : ""}
                  </p>
                </div>
              );
            }}
          />
          <Bar dataKey="count" fill={`url(#${id}-bar)`} radius={[6, 6, 0, 0]} maxBarSize={44} isAnimationActive={false} activeBar={{ fill: "#0d63dd", stroke: "#f2f7ff", strokeWidth: 1.2 }} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

// ---- 订阅测通的个数 ----

interface SubRow {
  t: number;
  usable: number;
  total: number;
}

const TOTAL_COLOR = "#9aa9c0";

export const subLegend = [
  { color: C1, label: "可用" },
  { color: TOTAL_COLOR, label: "节点总数" },
];

/** 一个订阅每次测延迟之后测通了几个。 */
export function SubUsableChart({ points, from, to, height = 180 }: { points: SubPoint[]; from: string; to: string; height?: number }) {
  const rows = useMemo<SubRow[]>(() => points.map((p) => ({ t: Date.parse(p.t), usable: p.usable, total: p.total })), [points]);
  const id = useId().replace(/:/g, "");
  const a = Date.parse(from);
  const b = Date.parse(to);
  const { ticks, format } = timeTicks(a, b);
  const last = rows[rows.length - 1];
  const summary = `可用节点数的变化，${mdhm(a)} 至 ${mdhm(b)}：共 ${rows.length} 次测试${last ? `，最近一次 ${last.total} 个节点中 ${last.usable} 个可用` : ""}。`;
  return (
    <Plot height={height} footer={[mdhm(a), mdhm(b)]} summary={summary}>
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={rows} margin={MARGIN}>
          <defs>
            <linearGradient id={`${id}-u`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={C1} stopOpacity={0.2} />
              <stop offset="100%" stopColor={C1} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke={GRID} strokeDasharray="2 4" vertical={false} />
          <XAxis dataKey="t" type="number" scale="time" domain={[a, b]} ticks={ticks.filter((_, i) => i % 2 === 0)} tickFormatter={format} {...AXIS} tickMargin={6} height={24} />
          <YAxis width={36} tickMargin={4} allowDecimals={false} tickCount={4} domain={[0, "auto"]} {...AXIS} />
          <Tooltip
            isAnimationActive={false}
            cursor={CURSOR}
            wrapperStyle={{ outline: "none" }}
            content={({ active, payload }: TipProps<SubRow>) => {
              const r = payload?.[0]?.payload;
              if (!active || !r) return null;
              return (
                <Tip
                  title={mdhm(r.t)}
                  rows={[
                    { color: C1, label: "可用", value: `${r.usable} 个` },
                    { color: TOTAL_COLOR, label: "节点总数", value: `${r.total} 个` },
                  ]}
                />
              );
            }}
          />
          <Area type="monotone" dataKey="usable" stroke="none" fill={`url(#${id}-u)`} dot={false} activeDot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="total" stroke={TOTAL_COLOR} strokeWidth={1.6} strokeDasharray="4 3" dot={false} activeDot={false} isAnimationActive={false} />
          <Line type="monotone" dataKey="usable" stroke={C1} strokeWidth={2} dot={{ r: 3, fill: C1, stroke: "#ffffff", strokeWidth: 1.5 }} activeDot={{ r: 4.5, stroke: "#ffffff", strokeWidth: 1.5, fill: C1 }} isAnimationActive={false} />
        </ComposedChart>
      </ResponsiveContainer>
    </Plot>
  );
}

// ---- 持续检测的时间线 ----

const watchColor: Record<WatchStatus, string> = { ok: "var(--watch-ok)", unreachable: "var(--watch-warn)", changed: "var(--watch-bad)" };
const watchText: Record<WatchStatus, string> = { ok: "正常", unreachable: "无法连接", changed: "出口已变更" };

export const watchLegend = (["ok", "unreachable", "changed"] as const).map((s) => ({ color: watchColor[s], label: watchText[s] }));

interface Segment {
  /** 没有检测的空档是 null。 */
  span: WatchSpan | null;
}

interface WatchRow {
  slot: string;
  segs: Segment[];
  [key: `s${number}`]: number;
}

/** 一行最多画多少段；检测结果来回跳得比这还多时，后面的不画。 */
const MAX_SEGMENTS = 80;

/**
 * 时间线：每个槽位一行，一段一种状态（横向堆叠的条形图）。
 * 不正常的那几段哪怕很短也至少占一点宽度，不然在七天的跨度里看不见；确切的起止时间在提示框里。
 */
export function WatchTimeline({ slots, watch, from, to }: { slots: string[]; watch: Record<string, WatchSpan[]>; from: string; to: string }) {
  const a = Date.parse(from);
  const b = Date.parse(to);
  const total = Math.max(b - a, 1);
  const { rows, width } = useMemo(() => {
    let width = 0;
    const rows = slots.map((slot) => {
      // 先排出每一段的起止，再填成“空档、状态、空档、状态…”的一行。
      const items: { start: number; end: number; span: WatchSpan }[] = [];
      let cursor = a;
      for (const span of watch[slot] ?? []) {
        const start = Math.max(Date.parse(span.from), cursor);
        const least = span.status === "ok" ? 5 * 60_000 : Math.max(5 * 60_000, total * 0.008);
        const end = Math.min(Math.max(Date.parse(span.to), start + least), b);
        if (end > start) items.push({ start, end, span });
        cursor = Math.max(cursor, end);
      }
      // 最后一段贴着右边界、又不正常时没有地方往右撑：往左撑，让“现在还不正常”看得见。
      const last = items[items.length - 1];
      if (last && last.span.status !== "ok") {
        last.start = Math.max(a, Math.min(last.start, last.end - Math.max(5 * 60_000, total * 0.008)));
        for (let i = items.length - 2; i >= 0; i--) items[i].end = Math.min(items[i].end, items[i + 1].start);
      }
      const row: WatchRow = { slot, segs: [] };
      let at = a;
      const push = (len: number, span: WatchSpan | null) => {
        if (len <= 0 || row.segs.length >= MAX_SEGMENTS) return;
        row[`s${row.segs.length}`] = len;
        row.segs.push({ span });
        at += len;
      };
      for (const it of items) {
        if (it.end <= it.start) continue;
        push(it.start - at, null);
        push(it.end - Math.max(it.start, at), it.span);
      }
      width = Math.max(width, row.segs.length);
      return row;
    });
    return { rows, width };
  }, [slots, watch, a, b, total]);
  const { ticks, format } = timeTicks(a, b);
  const long = total > 2 * 24 * 3600_000;
  const stamp = (iso: string) => (long ? mdhm(Date.parse(iso)) : hm(Date.parse(iso)));
  // 名称一列的宽度随最长的槽位名变化（上限 200），较长的名称不再被裁切。
  const nameWidth = Math.min(200, Math.max(72, slots.reduce((m, id) => Math.max(m, id.length), 0) * 7.5 + 18));

  return (
    <Plot height={rows.length * 38 + 44} footer={[mdhm(a), mdhm(b)]} summary={`持续检测的时间线，${mdhm(a)} 至 ${mdhm(b)}：槽位 ${slots.join("、")}。各时段的检测结果见各槽位的详情页。`}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={rows} layout="vertical" margin={{ top: 10, right: 14, bottom: 4, left: 8 }} barCategoryGap={0}>
          <CartesianGrid stroke={GRID} strokeDasharray="2 4" horizontal={false} />
          <XAxis type="number" domain={[0, total]} ticks={ticks.map((t) => t - a)} tickFormatter={(v: number) => format(a + v)} {...AXIS} tickMargin={6} height={24} />
          <YAxis type="category" dataKey="slot" width={nameWidth} {...AXIS} tick={{ fill: "#3f4b63", fontSize: 12, fontWeight: 600 }} />
          <Tooltip
            shared={false}
            isAnimationActive={false}
            cursor={false}
            wrapperStyle={{ outline: "none" }}
            content={({ active, payload }: TipProps<WatchRow>) => {
              const hit = payload?.[0];
              const span = hit?.payload?.segs[Number(String(hit.dataKey).slice(1))]?.span;
              if (!active || !hit?.payload || !span) return null;
              return (
                <Tip
                  title={`${hit.payload.slot} · ${stamp(span.from)} – ${stamp(span.to)}`}
                  rows={[
                    { color: watchColor[span.status], label: watchText[span.status], value: `持续 ${spanText(Date.parse(span.to) - Date.parse(span.from))}` },
                    { label: "检测", value: `${span.checks} 次` },
                    ...(span.exit_ip ? [{ label: "出口", value: span.exit_ip }] : []),
                  ]}
                />
              );
            }}
          />
          {Array.from({ length: width }, (_, i) => (
            <Bar
              key={i}
              dataKey={`s${i}`}
              stackId="w"
              barSize={16}
              isAnimationActive={false}
              shape={(p: { x?: number; y?: number; width?: number; height?: number; payload?: WatchRow }) => {
                const span = p.payload?.segs[i]?.span;
                if (!span || !p.width) return <g />;
                // 相邻两段之间留 1 像素的空，不描边。
                return <Rectangle x={(p.x ?? 0) + 0.5} y={p.y} width={Math.max(p.width - 1, 1.5)} height={p.height} radius={4} fill={watchColor[span.status]} />;
              }}
            />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </Plot>
  );
}
