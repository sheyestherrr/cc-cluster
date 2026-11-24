// 图表：都用 recharts 画，这里统一坐标轴、网格、提示框和配色，页面只管给数据。
// 配色：两个系列色是 index.css 里的 --chart-1 / --chart-2（下行、上行），状态用界面已有的 ok / warn / danger。
import type React from "react";
import { useMemo } from "react";
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Line, LineChart, Rectangle, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { HistoryPoint, SubPoint, WatchSpan, WatchStatus } from "@/lib/api";
import { bytesText, spanText } from "@/lib/utils";

const C1 = "var(--chart-1)";
const C2 = "var(--chart-2)";
const GRID = "rgb(var(--line-2))";
const TICK = { fill: "rgb(var(--muted))", fontSize: 10.5 } as const;
const AXIS = { tickLine: false, axisLine: false, tick: TICK } as const;

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

// ---- 提示框 ----

function TipBox({ title, children }: { title: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="rounded-lg px-3 py-2 text-[11.5px] leading-[18px] backdrop-blur" style={{ background: "var(--pop)", boxShadow: "var(--shadow)" }}>
      <div className="mb-0.5 text-muted">{title}</div>
      {children}
    </div>
  );
}

function TipRow({ color, label, value }: { color?: string; label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      {color && <span className="h-[2px] w-2.5 shrink-0 rounded-full" style={{ background: color }} />}
      <span className="font-semibold text-fg" style={{ fontVariantNumeric: "tabular-nums" }}>{value}</span>
      <span className="text-muted">{label}</span>
    </div>
  );
}

interface TipProps<Row> {
  active?: boolean;
  payload?: readonly { dataKey?: unknown; payload?: Row }[];
}

/** 图例：线形的色键加名字，放在图的上方。 */
export function Legend({ items }: { items: { color: string; label: string; shape?: "line" | "box" }[] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-[11.5px] text-fg-2">
      {items.map((it) => (
        <span key={it.label} className="flex items-center gap-1.5">
          <span className={it.shape === "box" ? "h-2 w-2 rounded-[2px]" : "h-[2px] w-3 rounded-full"} style={{ background: it.color }} />
          {it.label}
        </span>
      ))}
    </div>
  );
}

export const trafficLegend = [
  { color: C1, label: "下行" },
  { color: C2, label: "上行" },
];

// ---- 流量 ----

interface TrafficRow {
  t: number;
  down: number;
  up: number;
  conns: number;
}

/** 流量的面积图：每一档里的上下行字节。slot 给了就只画那个槽位的。 */
export function TrafficChart({ points, step, slot, height = 200 }: { points: HistoryPoint[]; step: number; slot?: string; height?: number }) {
  const rows = useMemo<TrafficRow[]>(
    () =>
      points.map((p) => {
        const v = slot ? p.slots?.[slot] : p;
        return { t: Date.parse(p.t), down: v?.down ?? 0, up: v?.up ?? 0, conns: v?.conns ?? 0 };
      }),
    [points, slot],
  );
  if (!rows.length) return null;
  const from = rows[0].t;
  const to = rows[rows.length - 1].t + step * 1000;
  const { ticks, format } = timeTicks(from, to);
  const long = to - from > 2 * 24 * 3600_000;
  const id = slot ? `tr-${slot}` : "tr-all";
  const yTicks = byteTicks(rows.reduce((m, r) => Math.max(m, r.down, r.up), 0));
  // 图矮的时候只标三个刻度。
  const shownTicks = height < 160 ? [yTicks[0], yTicks[2], yTicks[4]] : yTicks;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={rows} margin={{ top: 6, right: 4, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id={`${id}-1`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={C1} stopOpacity={0.3} />
            <stop offset="100%" stopColor={C1} stopOpacity={0.02} />
          </linearGradient>
          <linearGradient id={`${id}-2`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={C2} stopOpacity={0.22} />
            <stop offset="100%" stopColor={C2} stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke={GRID} />
        <XAxis dataKey="t" type="number" scale="time" domain={[from, to]} ticks={ticks} tickFormatter={format} {...AXIS} tickMargin={6} />
        <YAxis width={54} domain={[0, yTicks[4]]} ticks={shownTicks} tickFormatter={(v: number) => (v ? bytesText(v) : "0")} {...AXIS} />
        <Tooltip
          isAnimationActive={false}
          cursor={{ stroke: "rgb(var(--faint))", strokeWidth: 1 }}
          content={({ active, payload }: TipProps<TrafficRow>) => {
            const r = payload?.[0]?.payload;
            if (!active || !r) return null;
            return (
              <TipBox title={`${long ? mdhm(r.t) : hm(r.t)} – ${hm(r.t + step * 1000)}`}>
                <TipRow color={C1} value={bytesText(r.down)} label="下行" />
                <TipRow color={C2} value={bytesText(r.up)} label="上行" />
                {r.conns > 0 && <TipRow value={r.conns} label="条连接（峰值）" />}
              </TipBox>
            );
          }}
        />
        <Area type="monotone" dataKey="down" stroke={C1} strokeWidth={2} fill={`url(#${id}-1)`} dot={false} activeDot={{ r: 4, stroke: "rgb(var(--card))", strokeWidth: 2 }} isAnimationActive={false} />
        <Area type="monotone" dataKey="up" stroke={C2} strokeWidth={2} fill={`url(#${id}-2)`} dot={false} activeDot={{ r: 4, stroke: "rgb(var(--card))", strokeWidth: 2 }} isAnimationActive={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ---- 延迟分布 ----

export interface Bucket {
  label: string;
  count: number;
}

/** 柱状图：各档延迟里有多少个节点。 */
export function DelayBars({ buckets, total, height = 180 }: { buckets: Bucket[]; total: number; height?: number }) {
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={buckets} margin={{ top: 8, right: 4, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke={GRID} />
        <XAxis dataKey="label" {...AXIS} tickMargin={6} interval={0} />
        <YAxis width={30} allowDecimals={false} tickCount={4} {...AXIS} />
        <Tooltip
          isAnimationActive={false}
          cursor={{ fill: "rgb(var(--pill))", opacity: 0.7 }}
          content={({ active, payload }: TipProps<Bucket>) => {
            const r = payload?.[0]?.payload;
            if (!active || !r) return null;
            return (
              <TipBox title={`${r.label} 毫秒`}>
                <TipRow color={C1} value={`${r.count} 个`} label={total ? `占测通的 ${Math.round((r.count / total) * 100)}%` : ""} />
              </TipBox>
            );
          }}
        />
        <Bar dataKey="count" fill={C1} radius={[4, 4, 0, 0]} maxBarSize={24} isAnimationActive={false} />
      </BarChart>
    </ResponsiveContainer>
  );
}

// ---- 订阅测通的个数 ----

interface SubRow {
  t: number;
  usable: number;
  total: number;
}

export const subLegend = [
  { color: C1, label: "测通" },
  { color: "rgb(var(--faint))", label: "节点总数" },
];

/** 折线图：一个订阅每次测延迟之后测通了几个。 */
export function SubUsableChart({ points, from, to, height = 150 }: { points: SubPoint[]; from: string; to: string; height?: number }) {
  const rows = useMemo<SubRow[]>(() => points.map((p) => ({ t: Date.parse(p.t), usable: p.usable, total: p.total })), [points]);
  const a = Date.parse(from);
  const b = Date.parse(to);
  const { ticks, format } = timeTicks(a, b);
  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid vertical={false} stroke={GRID} />
        <XAxis dataKey="t" type="number" scale="time" domain={[a, b]} ticks={ticks} tickFormatter={format} {...AXIS} tickMargin={6} />
        <YAxis width={30} allowDecimals={false} tickCount={4} domain={[0, "auto"]} {...AXIS} />
        <Tooltip
          isAnimationActive={false}
          cursor={{ stroke: "rgb(var(--faint))", strokeWidth: 1 }}
          content={({ active, payload }: TipProps<SubRow>) => {
            const r = payload?.[0]?.payload;
            if (!active || !r) return null;
            return (
              <TipBox title={mdhm(r.t)}>
                <TipRow color={C1} value={`${r.usable} 个`} label="测通" />
                <TipRow color="rgb(var(--faint))" value={`${r.total} 个`} label="节点总数" />
              </TipBox>
            );
          }}
        />
        <Line type="linear" dataKey="total" stroke="rgb(var(--faint))" strokeWidth={1.5} dot={false} activeDot={false} isAnimationActive={false} />
        <Line type="linear" dataKey="usable" stroke={C1} strokeWidth={2} dot={{ r: 3.5, fill: C1, stroke: "rgb(var(--card))", strokeWidth: 2 }} activeDot={{ r: 5, stroke: "rgb(var(--card))", strokeWidth: 2 }} isAnimationActive={false} />
      </LineChart>
    </ResponsiveContainer>
  );
}

// ---- 持续检测的时间线 ----

// “连不上”在浅色下不用界面的 warn（偏棕，和红色的“出口变了”挨在一起分不清），另取一档更黄的。
const watchColor: Record<WatchStatus, string> = { ok: "rgb(var(--ok))", unreachable: "var(--watch-warn)", changed: "rgb(var(--danger))" };
const watchText: Record<WatchStatus, string> = { ok: "正常", unreachable: "连不上", changed: "出口变了" };

export const watchLegend = (["ok", "unreachable", "changed"] as const).map((s) => ({ color: watchColor[s], label: watchText[s], shape: "box" as const }));

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

  return (
    <ResponsiveContainer width="100%" height={rows.length * 28 + 30}>
      <BarChart data={rows} layout="vertical" margin={{ top: 2, right: 6, bottom: 0, left: 0 }} barCategoryGap={0}>
        <XAxis type="number" domain={[0, total]} ticks={ticks.map((t) => t - a)} tickFormatter={(v: number) => format(a + v)} {...AXIS} tickMargin={4} />
        <YAxis type="category" dataKey="slot" width={84} {...AXIS} tick={{ fill: "rgb(var(--fg-2))", fontSize: 11.5 }} />
        <Tooltip
          shared={false}
          isAnimationActive={false}
          cursor={false}
          content={({ active, payload }: TipProps<WatchRow>) => {
            const hit = payload?.[0];
            const span = hit?.payload?.segs[Number(String(hit.dataKey).slice(1))]?.span;
            if (!active || !hit?.payload || !span) return null;
            return (
              <TipBox title={`${hit.payload.slot} · ${stamp(span.from)} – ${stamp(span.to)}`}>
                <TipRow color={watchColor[span.status]} value={watchText[span.status]} label={`持续 ${spanText(Date.parse(span.to) - Date.parse(span.from))}，检测 ${span.checks} 次`} />
                {span.exit_ip && <TipRow value={span.exit_ip} label="出口" />}
              </TipBox>
            );
          }}
        />
        {Array.from({ length: width }, (_, i) => (
          <Bar
            key={i}
            dataKey={`s${i}`}
            stackId="w"
            barSize={10}
            isAnimationActive={false}
            shape={(p: { x?: number; y?: number; width?: number; height?: number; payload?: WatchRow }) => {
              const span = p.payload?.segs[i]?.span;
              if (!span || !p.width) return <g />;
              // 相邻两段之间留 1 像素的空，不描边。
              return <Rectangle x={(p.x ?? 0) + 0.5} y={p.y} width={Math.max(p.width - 1, 1)} height={p.height} radius={2} fill={watchColor[span.status]} />;
            }}
          />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}
