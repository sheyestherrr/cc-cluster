// 槽位详情里的“流量与连接”：此刻的速率、最近 24 小时的流量、当前的连接、连过的目标和最近结束的连接。
// 数据来自 cc-cluster web 进程里的采样；看得到的是连接这一层，看不到 HTTPS 的路径和内容。
import { ArrowDown, ArrowUp } from "lucide-react";
import type React from "react";
import { useState } from "react";
import { Legend, TrafficChart, trafficLegend } from "@/components/charts";
import { api, type History, type Slot, type SlotLive } from "@/lib/api";
import { usePolling } from "@/lib/live";
import { bytesText, cn, rateText, relativeTime, spanText } from "@/lib/utils";

type Tab = "now" | "hosts" | "ended";

function Figure({ label, value, icon }: { label: string; value: string; icon?: React.ReactNode }) {
  return (
    <div className="min-w-0 px-4 py-3">
      <div className="flex items-center gap-1 text-[11.5px] text-muted">{icon}{label}</div>
      <div className="mt-0.5 truncate text-[17px] font-semibold tracking-[-0.01em]">{value}</div>
    </div>
  );
}

function Amounts({ up, down }: { up?: number; down?: number }) {
  return (
    <span className="flex shrink-0 items-center gap-2 font-mono text-[11px] text-fg-2" style={{ fontVariantNumeric: "tabular-nums" }}>
      <span className="flex w-[68px] items-center justify-end gap-0.5"><ArrowDown className="h-3 w-3 text-muted" aria-label="下行" />{bytesText(down)}</span>
      <span className="flex w-[68px] items-center justify-end gap-0.5"><ArrowUp className="h-3 w-3 text-muted" aria-label="上行" />{bytesText(up)}</span>
    </span>
  );
}

const target = (host: string, port?: number) => (port ? `${host}:${port}` : host);

export function SlotTraffic({ s, live, history }: { s: Slot; live?: SlotLive; history: History | null }) {
  const wired = s.state === "ready" || s.state === "pending";
  const [tab, setTab] = useState<Tab>("now");
  const conns = usePolling(() => (wired ? api.slotConns(s.id) : Promise.resolve([])), 5000, [s.id, wired]);
  const log = usePolling(() => api.slotConnLog(s.id, 24, 100), 30_000, [s.id]);
  const total = history?.totals[s.id];
  const hasHistory = !!total && total.up + total.down > 0;
  const now = Date.now();

  const tabs: [Tab, string][] = [
    ["now", `当前连接${conns ? ` ${conns.length}` : ""}`],
    ["hosts", `连过的目标${log?.hosts.length ? ` ${log.hosts.length}` : ""}`],
    ["ended", `最近结束${log?.total ? ` ${log.total}` : ""}`],
  ];

  return (
    <section className="mt-5">
      <div className="group-label">流量与连接</div>
      <div className="list-card">
        <div className="grid grid-cols-3">
          <Figure label="下行" icon={<ArrowDown className="h-3 w-3" />} value={live ? rateText(live.down_bps) : "—"} />
          <Figure label="上行" icon={<ArrowUp className="h-3 w-3" />} value={live ? rateText(live.up_bps) : "—"} />
          <Figure label="连接" value={live ? `${live.conns} 条` : "—"} />
        </div>
        <div className="px-3 pb-2 pt-3">
          <div className="flex flex-wrap items-center justify-between gap-2 px-1 pb-1.5">
            <Legend items={trafficLegend} />
            <span className="text-[11.5px] text-muted">
              近 24 小时{hasHistory ? `：下行 ${bytesText(total.down)}，上行 ${bytesText(total.up)}` : ""}
            </span>
          </div>
          {hasHistory && history ? (
            <TrafficChart points={history.traffic} step={history.step} slot={s.id} height={132} />
          ) : (
            <div className="px-1 py-6 text-center text-[12px] leading-5 text-muted">
              {wired ? "还没有记录。流量每五分钟记一档，在这个网页或 cc-cluster watch 开着的时候记录。" : "这个槽位没有接线，没有流量。"}
            </div>
          )}
        </div>
      </div>

      <div className="mb-1.5 mt-3 flex gap-1" role="tablist" aria-label="连接">
        {tabs.map(([id, label]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
            className={cn("rounded-md px-2.5 py-1 text-[12px] active:!scale-100", tab === id ? "bg-pill font-medium text-fg" : "text-muted hover:text-fg")}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="list-card">
        {tab === "now" && (
          <>
            {!wired && <Empty>没有接线，没有连接。</Empty>}
            {wired && conns === null && <Empty>读取中…</Empty>}
            {wired && conns?.length === 0 && <Empty>现在没有连接。</Empty>}
            {conns?.slice(0, 60).map((c) => (
              <div key={c.id} className="flex items-center gap-3 px-4 py-2">
                <div className="min-w-0 flex-1">
                  <div className="selectable truncate font-mono text-[11.5px] text-fg">{target(c.host, c.port)}</div>
                  <div className="truncate text-[11px] text-muted">已持续 {spanText(now - Date.parse(c.start))}{c.chain ? ` · 经 ${c.chain}` : ""}{c.net === "udp" ? " · UDP" : ""}</div>
                </div>
                <Amounts up={c.up} down={c.down} />
              </div>
            ))}
            {!!conns && conns.length > 60 && <Empty>还有 {conns.length - 60} 条没有列出。</Empty>}
          </>
        )}
        {tab === "hosts" && (
          <>
            {log === null && <Empty>读取中…</Empty>}
            {log?.hosts.length === 0 && <Empty>近 24 小时里没有记录到结束的连接。</Empty>}
            {log?.hosts.slice(0, 40).map((h) => (
              <div key={h.host} className="flex items-center gap-3 px-4 py-2">
                <div className="min-w-0 flex-1">
                  <div className="selectable truncate font-mono text-[11.5px] text-fg">{h.host}</div>
                  <div className="truncate text-[11px] text-muted">{h.count} 条连接 · 最近一次{relativeTime(h.last)}</div>
                </div>
                <Amounts up={h.up} down={h.down} />
              </div>
            ))}
          </>
        )}
        {tab === "ended" && (
          <>
            {log === null && <Empty>读取中…</Empty>}
            {log?.records.length === 0 && <Empty>近 24 小时里没有记录到结束的连接。</Empty>}
            {log?.records.slice(0, 60).map((r, i) => (
              <div key={`${r.end}-${i}`} className="flex items-center gap-3 px-4 py-2">
                <div className="min-w-0 flex-1">
                  <div className="selectable truncate font-mono text-[11.5px] text-fg">{target(r.host, r.port)}</div>
                  <div className="truncate text-[11px] text-muted">{relativeTime(r.end)}结束 · 持续 {spanText(Date.parse(r.end) - Date.parse(r.start))}</div>
                </div>
                <Amounts up={r.up} down={r.down} />
              </div>
            ))}
            {!!log && log.total > log.records.length && <Empty>只列出最近的 {log.records.length} 条，共 {log.total} 条。</Empty>}
          </>
        )}
      </div>
      <p className="mt-1.5 px-1 text-[11.5px] leading-5 text-muted">
        这里只有连接这一层：目标的域名与端口、流量和时长，看不到 HTTPS 的网址路径和内容。连接靠十秒一次的快照记录，比这更短的可能不在其中；记录保留 30 天。
      </p>
    </section>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="px-4 py-3 text-[12px] text-muted">{children}</div>;
}
