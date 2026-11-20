// 本项目自己写的组件（不取自 Claude-Hydra）：落地出口 IP 的评估卡片。
// 数据是第三方接口对这个 IP 的看法，各家经常不一致，所以标记写成“命中的家数 / 给出判断的家数”。
import { Loader2, RefreshCw } from "lucide-react";
import { Tag } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import type { ExitReport } from "@/lib/api";
import { cn, relativeTime } from "@/lib/utils";

type Tone = "ok" | "warn" | "danger";

const toneText: Record<Tone, string> = { ok: "text-ok", warn: "text-warn", danger: "text-danger" };
const toneFill: Record<Tone, string> = { ok: "bg-ok", warn: "bg-warn", danger: "bg-danger" };

/** 全部六项标记，按这个顺序显示。 */
const FLAG_NAMES = ["机房", "代理", "住宅代理", "VPN", "Tor", "滥用"];

/** IP2Location 的用途类型代码。 */
const USAGE: Record<string, string> = {
  COM: "商业", ORG: "机构", GOV: "政府", MIL: "军事", EDU: "教育", LIB: "图书馆", CDN: "CDN",
  ISP: "固定宽带", MOB: "移动网络", DCH: "机房托管", SES: "搜索引擎", RSV: "保留地址",
};

const ABUSE_LEVEL: [string, string, Tone][] = [
  ["very low", "很低", "ok"], ["very high", "很高", "danger"], ["low", "低", "ok"], ["elevated", "偏高", "warn"], ["high", "高", "danger"],
];

function usageText(code?: string): string {
  if (!code) return "";
  return code.split("/").map((c) => USAGE[c.trim().toUpperCase()] ?? c.trim()).join(" / ");
}

/** 这个出口属于哪一类：住宅和移动网络是普通用户会有的，机房不是。 */
function kindOf(r: ExitReport): { text: string; tone?: Tone } {
  const flagged = (name: string) => r.flags.some((f) => f.name === name);
  if (flagged("Tor")) return { text: "Tor 出口", tone: "danger" };
  if (flagged("住宅代理")) return { text: "住宅代理", tone: "warn" };
  if (flagged("机房")) return { text: "机房", tone: "warn" };
  if (r.mobile) return { text: "移动网络", tone: "ok" };
  if (r.residential || r.user_type === "residential") return { text: "住宅宽带", tone: "ok" };
  return { text: usageText(r.usage_type) || r.user_type || r.company_type || "类型未知" };
}

function Score({ label, value, hint, pct, tone }: { label: string; value: string; hint: string; pct?: number; tone?: Tone }) {
  return (
    <div className="min-w-0 px-4 py-3" title={hint}>
      <div className="text-[11px] text-muted">{label}</div>
      <div className={cn("mt-0.5 truncate font-mono text-[15px] font-semibold tabular-nums", tone ? toneText[tone] : "text-fg-2")}>{value}</div>
      <div className="mt-1.5 h-[3px] overflow-hidden rounded-full bg-pill" aria-hidden>
        {pct !== undefined && tone && (
          <div className={cn("h-full origin-left rounded-full transition-transform duration-300 ease-soft motion-reduce:transition-none", toneFill[tone])} style={{ transform: `scaleX(${Math.max(0.04, Math.min(1, pct / 100))})` }} />
        )}
      </div>
    </div>
  );
}

interface Props {
  report?: ExitReport;
  /** 槽位最近一次复测通过的出口；与评估的不是同一个地址时提示。 */
  currentIP?: string;
  busy?: boolean;
  onRefresh?: () => void;
  /** 不能重新检测时的原因，显示在按钮的提示里。 */
  disabledWhy?: string;
}

export function ExitCard({ report: r, currentIP, busy, onRefresh, disabledWhy }: Props) {
  const head = (
    <div className="flex items-end justify-between">
      <div className="group-label">出口评估</div>
      {onRefresh && (
        <div className="pb-1">
          {busy ? (
            <span className="flex h-7 items-center gap-1.5 px-2 text-[12px] text-muted"><Loader2 className="h-3.5 w-3.5 animate-spin" />检测中</span>
          ) : (
            <Button variant="ghost" size="sm" onClick={onRefresh} disabled={!!disabledWhy} title={disabledWhy ?? "经这个落地的链路重新查询；对方看到的来源就是这个出口"}>
              <RefreshCw />
              重新检测
            </Button>
          )}
        </div>
      )}
    </div>
  );

  if (!r) {
    return (
      <section>
        {head}
        <div className="list-card">
          <div className="px-4 py-6 text-center text-[12.5px] leading-5 text-muted">
            还没有这个出口的评估。
            <br />
            启动使用它的槽位时会自动查询一次，之后一天内不重复查。
          </div>
        </div>
      </section>
    );
  }

  const kind = kindOf(r);
  const where = [r.country, r.region && r.region !== r.city ? r.region : "", r.city].filter(Boolean).join(" · ");
  const network = [r.asn ? `AS${r.asn}` : "", r.company || r.as_org, usageText(r.usage_type), r.connection_type].filter(Boolean).join(" · ");
  const trustTone: Tone | undefined = r.trust_score === undefined ? undefined : r.trust_score >= 70 ? "ok" : r.trust_score >= 40 ? "warn" : "danger";
  const fraudTone: Tone | undefined = r.fraud_score === undefined ? undefined : r.fraud_score <= 20 ? "ok" : r.fraud_score <= 60 ? "warn" : "danger";
  const abuse = ABUSE_LEVEL.find(([k]) => r.abuser_score?.toLowerCase().includes(k));
  const hit = new Map(r.flags.map((f) => [f.name, f]));
  const clean = FLAG_NAMES.filter((n) => !hit.has(n));
  const stale = currentIP && currentIP !== r.ip;

  return (
    <section>
      {head}
      <div className="list-card">
        <div className="px-4 py-3.5">
          <div className="flex items-center justify-between gap-3">
            <span className="selectable min-w-0 truncate font-mono text-[17px] font-semibold tracking-[-0.01em] tabular-nums">{r.ip}</span>
            <Tag tone={kind.tone}>{kind.text}</Tag>
          </div>
          <div className="mt-1 flex items-center gap-1.5 text-[12.5px] text-fg-2">
            {r.country_code && <span className="rounded bg-pill px-1 font-mono text-[10.5px] font-medium leading-4 text-fg-2">{r.country_code}</span>}
            <span className="selectable min-w-0 truncate">{where || "所在地未知"}</span>
          </div>
          {network && <div className="selectable mt-0.5 truncate text-[11.5px] text-muted" title={network}>{network}</div>}
          {stale && <div className="mt-2 rounded-lg bg-warn-soft px-2.5 py-1.5 text-[11.5px] leading-[18px] text-warn">这份评估是针对 {r.ip} 的；槽位最近一次复测的出口是 {currentIP}，重新检测后更新。</div>}
        </div>

        <div className="grid grid-cols-3 divide-x-[0.5px] divide-line">
          <Score label="信任分" value={r.trust_score === undefined ? "—" : String(r.trust_score)} pct={r.trust_score} tone={trustTone}
            hint="ip.net.coffee 给出的 0 到 100 的综合分，越高越好" />
          <Score label="欺诈分" value={r.fraud_score === undefined ? "—" : String(r.fraud_score)} pct={r.fraud_score} tone={fraudTone}
            hint="IP2Location 给出的 0 到 100 的欺诈分，越低越好" />
          <Score label="滥用程度" value={abuse ? abuse[1] : r.abuser_score ? r.abuser_score : "—"} tone={abuse?.[2]}
            pct={abuse ? { ok: 12, warn: 55, danger: 92 }[abuse[2]] : undefined}
            hint={r.abuser_score ? `这个地址所在网络的滥用比例：${r.abuser_score}` : "接口没有给出"} />
        </div>

        <div className="px-4 py-3">
          <div className="flex flex-wrap items-center gap-1.5">
            {r.flags.length === 0 && <Tag tone="ok">没有不利标记</Tag>}
            {FLAG_NAMES.filter((n) => hit.has(n)).map((n) => {
              const f = hit.get(n)!;
              return (
                <Tag key={n} tone={f.by.length === f.of ? "danger" : "warn"} title={`${f.by.join("、")} 把它标成了${n}；对这一项给出判断的共 ${f.of} 家`}>
                  {n} {f.by.length}/{f.of}
                </Tag>
              );
            })}
          </div>
          {r.flags.length > 0 && (
            <p className="mt-2 text-[11.5px] leading-[18px] text-muted">
              数字是命中的家数 / 给出判断的家数，各家数据经常不一致。{clean.length > 0 && `没有命中：${clean.join("、")}。`}
            </p>
          )}
          {(r.user_count !== undefined || r.static_ip_score !== undefined) && (
            <p className="mt-1 text-[11.5px] leading-[18px] text-muted" title="MaxMind 的估计：这个地址背后大约有多少用户，以及它有多固定（分数越高越固定）">
              MaxMind 估计{r.user_count !== undefined && `约 ${r.user_count} 个用户在用这个地址`}{r.user_count !== undefined && r.static_ip_score !== undefined && "，"}{r.static_ip_score !== undefined && `固定程度 ${r.static_ip_score}`}。
            </p>
          )}
        </div>

        <div className="flex items-center justify-between gap-3 px-4 py-2.5 text-[11.5px] text-muted">
          <span className="min-w-0 truncate">来源 {r.sources.join("、")}{r.timezone ? ` · ${r.timezone}` : ""}</span>
          <span className="shrink-0" title={r.checked_at}>{relativeTime(r.checked_at)}检测</span>
        </div>
      </div>
    </section>
  );
}
