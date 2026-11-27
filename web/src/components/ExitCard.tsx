// 落地出口 IP 的评估卡片。
// 数据是第三方接口对这个 IP 的看法，各家经常不一致，所以标记写成“命中的家数 / 给出判断的家数”。
import { Loader2, RefreshCw } from "lucide-react";
import { Flag } from "@/components/Flag";
import { Tag } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
    <div className="kv" title={hint}>
      <span>{label}</span>
      <strong className={cn("num !text-[20px] !font-bold", tone && toneText[tone])}>{value}</strong>
      <div className="mt-2 h-1 overflow-hidden rounded-full bg-pill" aria-hidden>
        {pct !== undefined && tone && <div className={cn("h-full origin-left rounded-full", toneFill[tone])} style={{ transform: `scaleX(${Math.max(0.04, Math.min(1, pct / 100))})` }} />}
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
  className?: string;
}

export function ExitCard({ report: r, currentIP, busy, onRefresh, disabledWhy, className }: Props) {
  const head = (
    <div className="panel-head">
      <div>
        <h3>出口评估</h3>
        <p>第三方接口对该出口地址的评估{r ? `，${relativeTime(r.checked_at)}检测` : ""}</p>
      </div>
      {onRefresh && (
        <Button variant="outline" size="sm" onClick={onRefresh} disabled={busy || !!disabledWhy} title={disabledWhy ?? "经该落地的链路重新查询，查询请求的来源即该出口"}>
          {busy ? <Loader2 className="spin" /> : <RefreshCw />}
          {busy ? "检测中…" : "重新检测"}
        </Button>
      )}
    </div>
  );

  if (!r) {
    return (
      <Card className={cn("panel", className)}>
        {head}
        <div className="empty-box plain flex-1">
          <p className="text-center">暂无该出口的评估。<br />启动使用该落地的槽位时自动查询，24 小时内不重复查询。</p>
        </div>
      </Card>
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
    <Card className={cn("panel", className)}>
      {head}
      <div>
        <div className="flex flex-wrap items-center gap-3">
          <span className="mono num min-w-0 truncate font-display !text-[24px] font-bold tracking-[-0.01em]">{r.ip}</span>
          <Tag tone={kind.tone}>{kind.text}</Tag>
        </div>
        <div className="mt-1.5 flex items-center gap-2 text-[14px] text-fg-2">
          <Flag code={r.country_code} className="h-[14px]" />
          <span className="min-w-0 truncate">{where || "所在地未知"}</span>
        </div>
        {network && <div className="mt-0.5 truncate text-[12.5px] text-muted" title={network}>{network}</div>}
      </div>
      {stale && <div className="callout callout-warning">该评估针对 {r.ip}，槽位最近一次检测到的出口为 {currentIP}。重新检测后更新。</div>}

      <div className="grid grid-cols-3 gap-2.5">
        <Score label="信任分" value={r.trust_score === undefined ? "—" : String(r.trust_score)} pct={r.trust_score} tone={trustTone} hint="ip.net.coffee 的综合评分（0–100），越高越好" />
        <Score label="欺诈分" value={r.fraud_score === undefined ? "—" : String(r.fraud_score)} pct={r.fraud_score} tone={fraudTone} hint="IP2Location 的欺诈评分（0–100），越低越好" />
        <Score
          label="滥用程度"
          value={abuse ? abuse[1] : r.abuser_score ? r.abuser_score : "—"}
          tone={abuse?.[2]}
          pct={abuse ? { ok: 12, warn: 55, danger: 92 }[abuse[2]] : undefined}
          hint={r.abuser_score ? `该地址所在网络的滥用比例：${r.abuser_score}` : "接口未提供"}
        />
      </div>

      <div>
        <div className="flex flex-wrap items-center gap-1.5">
          {r.flags.length === 0 && <Tag tone="ok">无不利标记</Tag>}
          {FLAG_NAMES.filter((n) => hit.has(n)).map((n) => {
            const f = hit.get(n)!;
            return (
              <Tag key={n} tone={f.by.length === f.of ? "danger" : "warn"} title={`${f.by.join("、")} 标记为${n}；共 ${f.of} 个数据源给出判断`}>
                {n} {f.by.length}/{f.of}
              </Tag>
            );
          })}
        </div>
        {r.flags.length > 0 && (
          <p className="field-hint mt-2">数字为标记该项的数据源数 / 给出判断的数据源数，各数据源的结果可能不一致。{clean.length > 0 && `未标记：${clean.join("、")}。`}</p>
        )}
        {(r.user_count !== undefined || r.static_ip_score !== undefined) && (
          <p className="field-hint" title="MaxMind 的估计：使用该地址的用户数，以及地址的固定程度（分数越高越固定）">
            MaxMind 估计：{r.user_count !== undefined && `约 ${r.user_count} 个用户使用该地址`}{r.user_count !== undefined && r.static_ip_score !== undefined && "，"}{r.static_ip_score !== undefined && `固定程度 ${r.static_ip_score}`}。
          </p>
        )}
      </div>

      <p className="field-hint mt-auto truncate" title={r.checked_at}>来源 {r.sources.join("、")}{r.timezone ? ` · ${r.timezone}` : ""}</p>
    </Card>
  );
}
