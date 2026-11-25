// 槽位详情里的“流量与连接”：此刻的速率、最近 24 小时的流量、当前的连接、连过的目标和最近结束的连接。
// 数据来自 cc-cluster web 进程里的采样；看得到的是连接这一层，看不到 HTTPS 的路径和内容。
// 版面参考了 Resin（MIT）平台详情页的监控面板：一排指标卡、趋势图、下面是表格。
import { ArrowDownToLine, ArrowUpFromLine, Cable, Info } from "lucide-react";
import type React from "react";
import { useState } from "react";
import { ChartEmpty, ConnsChart, Legend, TrafficChart, trafficLegend } from "@/components/charts";
import { Tabs } from "@/components/PageHeader";
import { Card } from "@/components/ui/card";
import { api, type History, type Slot, type SlotLive } from "@/lib/api";
import { usePolling } from "@/lib/live";
import { bytesText, rateText, relativeTime, spanText } from "@/lib/utils";

type Tab = "now" | "hosts" | "ended";

function Kpi({ icon, tone, label, value, sub }: { icon: React.ReactNode; tone: string; label: string; value: string; sub: string }) {
  return (
    <Card className="platform-monitor-kpi-card">
      <div className={`dashboard-kpi-icon ${tone}`}>{icon}</div>
      <div className="min-w-0">
        <p className="platform-monitor-kpi-label">{label}</p>
        <p className="platform-monitor-kpi-value num truncate">{value}</p>
        <p className="platform-monitor-kpi-sub">{sub}</p>
      </div>
    </Card>
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
  const peak = history?.traffic.reduce((m, p) => Math.max(m, p.slots?.[s.id]?.conns ?? 0), 0) ?? 0;
  const now = Date.now();
  const none = wired ? "还没有记录。流量每五分钟记一档，在这个网页或 cc-cluster watch 开着的时候记录。" : "这个槽位没有接线，没有流量。";

  return (
    <div className="flex flex-col gap-3.5">
      <div className="grid gap-3 min-[900px]:grid-cols-3">
        <Kpi icon={<ArrowDownToLine size={20} />} tone="waves" label="此刻下行" value={live ? rateText(live.down_bps) : "—"} sub={`近 24 小时 ${bytesText(total?.down)}`} />
        <Kpi icon={<ArrowUpFromLine size={20} />} tone="shield" label="此刻上行" value={live ? rateText(live.up_bps) : "—"} sub={`近 24 小时 ${bytesText(total?.up)}`} />
        <Kpi icon={<Cable size={20} />} tone="lease" label="当前连接" value={live ? `${live.conns} 条` : "—"} sub={`近 24 小时峰值 ${peak} 条`} />
      </div>

      <div className="grid gap-3 min-[1100px]:grid-cols-3">
        <div className="flex min-w-0 flex-col gap-2.5 min-[1100px]:col-span-2">
          <div className="dashboard-panel-header">
            <h3>流量趋势</h3>
            <p>最近 24 小时，每五分钟一档</p>
          </div>
          {hasHistory && history ? <TrafficChart points={history.traffic} step={history.step} slot={s.id} height={240} /> : <ChartEmpty height={240}>{none}</ChartEmpty>}
          <Legend items={trafficLegend} />
        </div>
        <div className="flex min-w-0 flex-col gap-2.5">
          <div className="dashboard-panel-header">
            <h3>连接峰值</h3>
            <p>每一档里同时连接数的最大值</p>
          </div>
          {peak > 0 && history ? <ConnsChart points={history.traffic} step={history.step} slot={s.id} height={240} /> : <ChartEmpty height={240}>没有连接的记录。</ChartEmpty>}
          <Legend items={[{ color: "var(--chart-3)", label: "连接数" }]} />
        </div>
      </div>

      <div className="mt-2 flex flex-wrap items-center justify-between gap-3">
        <Tabs
          label="连接"
          value={tab}
          onChange={setTab}
          items={[
            ["now", `当前连接${conns ? ` ${conns.length}` : ""}`],
            ["hosts", `连过的目标${log?.hosts.length ? ` ${log.hosts.length}` : ""}`],
            ["ended", `最近结束${log?.total ? ` ${log.total}` : ""}`],
          ]}
        />
      </div>

      <div className="data-table-wrap">
        {tab === "now" && (
          <ConnTable
            head={["目标", "已持续", "经由", "下行", "上行"]}
            empty={!wired ? "没有接线，没有连接。" : conns === null ? "读取中…" : conns.length === 0 ? "现在没有连接。" : ""}
            rows={(conns ?? []).slice(0, 80).map((c) => ({
              key: c.id,
              cells: [<span className="mono cell-main">{target(c.host, c.port)}{c.net === "udp" && <span className="badge badge-muted ml-2">UDP</span>}</span>, spanText(now - Date.parse(c.start)), c.chain || "—", bytesText(c.down), bytesText(c.up)],
            }))}
            more={conns && conns.length > 80 ? `还有 ${conns.length - 80} 条没有列出。` : ""}
          />
        )}
        {tab === "hosts" && (
          <ConnTable
            head={["目标", "连接数", "最近一次", "下行", "上行"]}
            empty={log === null ? "读取中…" : log.hosts.length === 0 ? "近 24 小时里没有记录到结束的连接。" : ""}
            rows={(log?.hosts ?? []).slice(0, 60).map((h) => ({
              key: h.host,
              cells: [<span className="mono cell-main">{h.host}</span>, `${h.count} 条`, relativeTime(h.last), bytesText(h.down), bytesText(h.up)],
            }))}
          />
        )}
        {tab === "ended" && (
          <ConnTable
            head={["目标", "结束于", "持续", "下行", "上行"]}
            empty={log === null ? "读取中…" : log.records.length === 0 ? "近 24 小时里没有记录到结束的连接。" : ""}
            rows={(log?.records ?? []).slice(0, 80).map((r, i) => ({
              key: `${r.end}-${i}`,
              cells: [<span className="mono cell-main">{target(r.host, r.port)}</span>, relativeTime(r.end), spanText(Date.parse(r.end) - Date.parse(r.start)), bytesText(r.down), bytesText(r.up)],
            }))}
            more={log && log.total > log.records.length ? `只列出最近的 ${log.records.length} 条，共 ${log.total} 条。` : ""}
          />
        )}
      </div>

      <div className="callout callout-info">
        <Info size={15} />
        <span>这里只有连接这一层：目标的域名与端口、流量和时长，看不到 HTTPS 的网址路径和内容。连接靠十秒一次的快照记录，比这更短的可能不在其中；记录保留 30 天。</span>
      </div>
    </div>
  );
}

function ConnTable({ head, rows, empty, more }: { head: string[]; rows: { key: string; cells: React.ReactNode[] }[]; empty: string; more?: string }) {
  if (empty) return <div className="empty-box plain"><p>{empty}</p></div>;
  return (
    <>
      <table className="data-table compact">
        <thead>
          <tr>{head.map((h, i) => <th key={h} className={i >= 3 ? "right" : undefined}>{h}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key}>
              {r.cells.map((c, i) => <td key={i} className={i >= 3 ? "right num" : i ? "cell-sub" : undefined}>{c}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
      {more && <p className="field-hint px-3 pb-2">{more}</p>}
    </>
  );
}
