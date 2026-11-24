// 改编自 Claude-Hydra（MIT）app/src/pages/EnvironmentsPage.tsx 与 app/src/components/EnvironmentDialog.tsx，v1.1.6：
// 沿用列表、空状态与分组表单的结构，内容换成 cc-cluster 的网关、节点与订阅。
import { Gauge, Globe2, Loader2, LogOut, MoreHorizontal, Pencil, Play, Plus, Power, RefreshCw, Rss, Router, Trash2, Waypoints } from "lucide-react";
import type React from "react";
import { useCallback, useEffect, useState } from "react";
import { ExitCard } from "@/components/ExitCard";
import { RegionIcon } from "@/components/Flag";
import { Menu, type MenuEntry } from "@/components/Menu";
import { NOTE_PLACEHOLDER, NoteDialog, NoteText, type NoteTarget } from "@/components/NoteDialog";
import { DetailEmpty, DetailGrid, DetailHeader, ErrorBox, KeyValue, Line, Page, PageHeader, RowTitle, Tag, selectableRow } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useToast } from "@/components/ui/toast";
import { ApiError, ID_HINT, NOTE_MAX, api, errorText, type Delay, type Gateway, type NodeInfo, type NodeInput, type Role, type State, type Sub } from "@/lib/api";
import { cn, relativeTime } from "@/lib/utils";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
}

type Failure = { text: string; log: string[] } | null;
type Removing = { kind: "gateway" | "node" | "sub"; id: string; note: string } | null;
type Selection = { kind: "gateway" | "node" | "sub"; id: string };

const roleText: Record<Role, string> = { transit: "中转", landing: "落地" };

const gatewayStatus = (g: Gateway) =>
  g.container === "running" ? (g.core_ready ? "运行中" : "核心未就绪") : g.container === "absent" ? "未创建" : g.container === "unknown" ? "状态未知" : "已停止";

/** 详情栏里显示哪个对象：选中的那个；没选过或它已被删除时，优先显示一个落地。 */
function pick(state: State, sel: Selection | null): Selection | null {
  const exists = (s: Selection) =>
    s.kind === "gateway" ? state.gateways.some((g) => g.id === s.id) : s.kind === "node" ? state.nodes.some((n) => n.id === s.id) : state.subs.some((x) => x.id === s.id);
  if (sel && exists(sel)) return sel;
  const node = state.nodes.find((n) => n.role === "landing") ?? state.nodes[0];
  if (node) return { kind: "node", id: node.id };
  if (state.gateways[0]) return { kind: "gateway", id: state.gateways[0].id };
  if (state.subs[0]) return { kind: "sub", id: state.subs[0].id };
  return null;
}

export function NetworkPage({ state, loadError, refresh }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [dialog, setDialog] = useState<"gateway" | "node" | "sub" | null>(null);
  const [browsing, setBrowsing] = useState<string | null>(null);
  const [removing, setRemoving] = useState<Removing>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  const [sel, setSel] = useState<Selection | null>(null);

  const run = async (key: string, label: string, action: () => Promise<unknown>, done?: string | ((r: unknown) => string)) => {
    setBusy((b) => ({ ...b, [key]: label }));
    setFailure(null);
    try {
      const r = await action();
      if (done) toast(typeof done === "string" ? done : done(r));
    } catch (e) {
      setFailure({ text: errorText(e), log: e instanceof ApiError ? e.log : [] });
    } finally {
      setBusy((b) => {
        const { [key]: _, ...rest } = b;
        return rest;
      });
      refresh();
    }
  };

  const remove = (r: NonNullable<Removing>) => {
    setRemoving(null);
    const action = r.kind === "gateway" ? () => api.removeGateway(r.id) : r.kind === "node" ? () => api.removeNode(r.id) : () => api.removeSub(r.id);
    run(`${r.kind}:${r.id}`, "删除中", action, `已删除 ${r.id}`);
  };

  const created = async (text: string) => {
    setDialog(null);
    toast(text);
    await refresh();
  };

  const empty = !!state && !state.gateways.length && !state.nodes.length && !state.subs.length;
  const current = state ? pick(state, sel) : null;
  const isCurrent = (kind: Selection["kind"], id: string) => current?.kind === kind && current.id === id;
  const select = (kind: Selection["kind"], id: string) => () => setSel({ kind, id });

  let side: React.ReactNode = <DetailEmpty>{state ? "添加节点或网关后，这里显示所选对象的详情；落地还会显示出口 IP 的评估。" : "读取中…"}</DetailEmpty>;
  if (state && current?.kind === "node") {
    const n = state.nodes.find((x) => x.id === current.id)!;
    side = (
      <NodeDetail
        key={`node:${n.id}`}
        n={n}
        state={state}
        busy={busy[`node:${n.id}`]}
        onReport={() => run(`node:${n.id}`, "检测中", () => api.nodeReport(n.id), `已更新 ${n.id} 的出口评估`)}
      />
    );
  } else if (state && current?.kind === "gateway") {
    side = <GatewayDetail key={`gateway:${current.id}`} g={state.gateways.find((x) => x.id === current.id)!} state={state} refresh={refresh} />;
  } else if (state && current?.kind === "sub") {
    side = <SubDetail key={`sub:${current.id}`} s={state.subs.find((x) => x.id === current.id)!} state={state} onBrowse={() => setBrowsing(current.id)} />;
  }

  return (
    <Page side={side}>
      <PageHeader
        title="节点与网关"
        subtitle="落地是槽位的出口，中转是多个槽位共用的第一跳；网关容器把槽位的流量经中转送到各自的落地。"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setDialog("sub")}><Rss />导入订阅</Button>
            <Button variant="outline" size="sm" onClick={() => setDialog("node")}><Plus />添加节点</Button>
            <Button size="sm" onClick={() => setDialog("gateway")} disabled={!state}><Plus />新建网关</Button>
          </>
        }
      />

      <div className="space-y-5 px-5">
        {loadError && <ErrorBox text={loadError} />}
        {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}

        {empty && (
          <button onClick={() => setDialog("node")} className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed border-line px-4 py-10 text-muted hover:border-faint hover:text-fg active:!scale-100">
            <Globe2 className="h-5 w-5" />
            <span className="text-[13px] font-medium">还没有节点</span>
            <span className="text-[12px]">先添加一个落地节点（槽位的出口）；需要中转时导入订阅，从里面按延迟挑一个</span>
          </button>
        )}

        {!!state?.gateways.length && (
          <section>
            <div className="group-label">网关</div>
            <div className="list-card">
              {state.gateways.map((g) => (
                <GatewayRow
                  key={g.id}
                  g={g}
                  selected={isCurrent("gateway", g.id)}
                  onSelect={select("gateway", g.id)}
                  busy={busy[`gateway:${g.id}`]}
                  onUp={() => run(`gateway:${g.id}`, "启动中", () => api.gatewayUp(g.id), `网关 ${g.id} 已就绪`)}
                  onDown={(force) => run(`gateway:${g.id}`, "停止中", () => api.gatewayDown(g.id, force), `已停止网关 ${g.id}`)}
                  onNote={() => setNoting({ kind: "gateways", what: "网关", id: g.id, note: g.note })}
                  onDelete={() => setRemoving({ kind: "gateway", id: g.id, note: g.slots.length ? `它上面还有槽位（${g.slots.join("、")}），需要先删除这些槽位。` : "会删除它的容器和配置。" })}
                />
              ))}
            </div>
          </section>
        )}

        {!!state?.nodes.length && (
          <section>
            <div className="group-label">节点</div>
            <div className="list-card">
              {state.nodes.map((n) => (
                <NodeRow
                  key={n.id}
                  n={n}
                  selected={isCurrent("node", n.id)}
                  onSelect={select("node", n.id)}
                  busy={busy[`node:${n.id}`]}
                  onNote={() => setNoting({ kind: "nodes", what: "节点", id: n.id, note: n.note })}
                  onResolve={() => run(`node:${n.id}`, "解析中", () => api.resolveNode(n.id), (r) => `${n.id} 的首跳地址：${(r as { data: { connect_ipv4: string } }).data.connect_ipv4}`)}
                  onDelete={() => setRemoving({ kind: "node", id: n.id, note: n.used_by.length ? `它正被 ${n.used_by.join("、")} 使用，需要先解除。` : "此节点没有被使用。" })}
                />
              ))}
            </div>
          </section>
        )}

        {!!state?.subs.length && (
          <section>
            <div className="group-label">订阅</div>
            <div className="list-card">
              {state.subs.map((s) => (
                <SubRow
                  key={s.id}
                  s={s}
                  selected={isCurrent("sub", s.id)}
                  onSelect={select("sub", s.id)}
                  busy={busy[`sub:${s.id}`]}
                  onBrowse={() => setBrowsing(s.id)}
                  onNote={() => setNoting({ kind: "subs", what: "订阅", id: s.id, note: s.note })}
                  onRefresh={() =>
                    run(`sub:${s.id}`, "刷新中", () => api.refreshSub(s.id), (r) => {
                      const d = (r as Awaited<ReturnType<typeof api.refreshSub>>).data;
                      const parts = [`订阅 ${s.id} 现有 ${d.Total} 个节点`];
                      if (d.Updated?.length) parts.push(`已更新节点 ${d.Updated.join("、")}（重新启动网关或槽位后生效）`);
                      if (d.Renamed?.length) parts.push(`${d.Renamed.join("、")} 在订阅里改了名，已跟着改过来`);
                      if (d.Missing?.length) parts.push(`订阅里已经没有 ${d.Missing.join("、")} 对应的节点，它们保持原样`);
                      return parts.join("；");
                    })
                  }
                  onDelete={() => setRemoving({ kind: "sub", id: s.id, note: "已经从它里面选用的节点不受影响，只是不再随订阅刷新。" })}
                />
              ))}
            </div>
          </section>
        )}
      </div>

      {state && <AddGatewayDialog open={dialog === "gateway"} state={state} onClose={() => setDialog(null)} onCreated={(id) => created(`已登记网关 ${id}；启动绑定它的槽位时会自动拉起。`)} />}
      <AddNodeDialog open={dialog === "node"} onClose={() => setDialog(null)} onCreated={(id) => created(`已登记节点 ${id}。`)} />
      <AddSubDialog open={dialog === "sub"} onClose={() => setDialog(null)} onCreated={async (id, count) => {
        await created(`已导入订阅 ${id}：${count} 个节点。`);
        setBrowsing(id);
      }} />
      {state && <SubDialog sub={browsing} state={state} onClose={() => setBrowsing(null)} onChanged={refresh} />}
      <NoteDialog target={noting} onClose={() => setNoting(null)} onSaved={(text) => {
        setNoting(null);
        toast(text);
        void refresh();
      }} />

      <Dialog
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={`删除 ${removing?.id ?? ""}？`}
        description={removing?.note}
        footer={
          <>
            <Button variant="outline" onClick={() => setRemoving(null)}>取消</Button>
            <Button variant="destructive" onClick={() => removing && remove(removing)}>删除</Button>
          </>
        }
      />
    </Page>
  );
}

interface Selectable {
  selected: boolean;
  onSelect: () => void;
}

function RowShell({ icon, region, regionFrom, title, note, tags, detail, side, busy, action, menu, selected, onSelect }: Selectable & {
  icon: React.ComponentType<{ className?: string }>;
  /** 知道地区时行首显示国旗，不知道时显示 icon。 */
  region?: string;
  regionFrom?: string;
  title: string;
  note?: string;
  tags?: React.ReactNode;
  detail: React.ReactNode;
  side?: React.ReactNode;
  busy?: string;
  action?: React.ReactNode;
  menu: (MenuEntry | "sep")[];
}) {
  return (
    <div {...selectableRow(selected, onSelect)}>
      <RegionIcon code={region} from={regionFrom} icon={icon} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <RowTitle selected={selected} onSelect={onSelect}>{title}</RowTitle>
          <NoteText note={note} />
          {tags}
        </div>
        <div className="selectable truncate text-[11.5px] text-muted">{detail}</div>
      </div>
      {side && <span className="hidden max-w-[160px] shrink-0 truncate text-[11.5px] text-muted min-[1500px]:inline">{side}</span>}
      {busy ? (
        <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          {busy}
        </span>
      ) : action}
      <Menu
        trigger={(toggle) => (
          <Button variant="ghost" size="iconSm" aria-label="更多" onClick={toggle} disabled={!!busy}>
            <MoreHorizontal />
          </Button>
        )}
        items={menu}
      />
    </div>
  );
}

function GatewayRow({ g, busy, onUp, onDown, onNote, onDelete, ...sel }: Selectable & { g: Gateway; busy?: string; onUp: () => void; onDown: (force: boolean) => void; onNote: () => void; onDelete: () => void }) {
  const running = g.container === "running";
  const status = running ? "运行中" : g.container === "absent" ? "未创建" : g.container === "unknown" ? "状态未知" : "已停止";
  return (
    <RowShell
      {...sel}
      icon={Router}
      title={g.id}
      note={g.note}
      tags={
        <>
          {g.transit ? <Tag tone="env" title="中转节点">{g.transit}</Tag> : <Tag title="不经中转，各槽位直接连自己的出口节点">不经中转</Tag>}
          {running && !g.core_ready && <Tag tone="warn">核心未就绪</Tag>}
          {running && g.core_ready && !g.config_current && <Tag tone="warn" title="节点或槽位的设置变过，重新启动网关或槽位后生效">配置待更新</Tag>}
        </>
      }
      detail={
        <span className="flex items-center gap-1.5">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", running && g.core_ready ? "bg-ok shadow-[0_0_0_3px_rgb(var(--ok-soft))]" : "bg-faint")} />
          {status} · 出口网络 {g.egress_network} · 地址池 {g.pool}
        </span>
      }
      side={g.slots.length ? `槽位 ${g.slots.join("、")}` : "没有槽位"}
      busy={busy}
      action={
        <Button variant="outline" size="sm" className="min-w-[72px]" onClick={running ? () => onDown(false) : onUp} disabled={g.container === "unknown"}>
          {running ? <><Power />停止</> : <><Play className="!h-3 !w-3" />启动</>}
        </Button>
      }
      menu={[
        { label: "重新应用配置", icon: RefreshCw, onSelect: onUp },
        { label: "强制停止（槽位会断网）", icon: LogOut, onSelect: () => onDown(true), disabled: !running },
        { label: "备注…", icon: Pencil, onSelect: onNote },
        "sep",
        { label: "删除…", icon: Trash2, danger: true, onSelect: onDelete },
      ]}
    />
  );
}

/** 出口评估的一句话，放在落地那一行里。 */
function reportBrief(n: NodeInfo): string {
  const r = n.exit_report;
  if (!r) return "";
  const where = [r.country_code || r.country, r.city].filter(Boolean).join(" ");
  const flags = r.flags.length ? r.flags.map((f) => f.name).join("、") : "无不利标记";
  return [`出口 ${r.ip}`, where, r.trust_score !== undefined ? `信任分 ${r.trust_score}` : "", flags].filter(Boolean).join(" · ");
}

function NodeRow({ n, busy, onResolve, onNote, onDelete, ...sel }: Selectable & { n: NodeInfo; busy?: string; onResolve: () => void; onNote: () => void; onDelete: () => void }) {
  const parts = [`${n.type} · ${n.server}:${n.port}`];
  const brief = reportBrief(n);
  if (brief) parts.push(brief);
  else if (n.connect_ipv4) parts.push(`首跳 ${n.connect_ipv4}`);
  return (
    <RowShell
      {...sel}
      icon={n.role === "landing" ? Globe2 : Waypoints}
      region={n.region}
      regionFrom={n.region_from}
      title={n.id}
      note={n.note}
      tags={
        <>
          <Tag tone={n.role === "landing" ? "env" : undefined}>{roleText[n.role]}</Tag>
          {n.source && <Tag title={`取自订阅 ${n.source.sub}`}>{n.source.name}</Tag>}
          {n.tz_fallback && <Tag tone="warn" title="没有探测到出口所在的时区，暂用兜底值">时区兜底</Tag>}
        </>
      }
      detail={parts.join(" · ")}
      side={n.used_by.length ? n.used_by.join("、") : "未使用"}
      busy={busy}
      menu={[
        { label: "重新解析首跳地址", icon: RefreshCw, onSelect: onResolve, disabled: !n.connect_auto },
        { label: "备注…", icon: Pencil, onSelect: onNote },
        "sep",
        { label: "删除…", icon: Trash2, danger: true, onSelect: onDelete },
      ]}
    />
  );
}

function SubRow({ s, busy, onBrowse, onRefresh, onNote, onDelete, ...sel }: Selectable & { s: Sub; busy?: string; onBrowse: () => void; onRefresh: () => void; onNote: () => void; onDelete: () => void }) {
  return (
    <RowShell
      {...sel}
      icon={Rss}
      title={s.id}
      note={s.note}
      detail={`${s.count} 个节点 · ${s.tested_at ? `${s.usable} 个测通（${relativeTime(s.tested_at)}测）` : "还没测过延迟"} · ${relativeTime(s.fetched_at)}拉取`}
      busy={busy}
      action={<Button variant="outline" size="sm" onClick={onBrowse}>选节点…</Button>}
      menu={[
        { label: "重新拉取", icon: RefreshCw, onSelect: onRefresh },
        { label: "备注…", icon: Pencil, onSelect: onNote },
        "sep",
        { label: "删除…", icon: Trash2, danger: true, onSelect: onDelete },
      ]}
    />
  );
}

function IconBadge({ icon }: { icon: React.ComponentType<{ className?: string }> }) {
  return <RegionIcon icon={icon} size="detail" />;
}

/** 详情栏：一个节点。落地显示出口评估，中转只有连接信息。 */
function NodeDetail({ n, state, busy, onReport }: { n: NodeInfo; state: State; busy?: string; onReport: () => void }) {
  const landing = n.role === "landing";
  const slots = state.slots.filter((s) => s.landing === n.id);
  // 出口评估要经链路查询：得有一个用着这个落地的槽位，它的网关在运行。
  const reachable = slots.some((s) => {
    const g = state.gateways.find((x) => x.id === s.gateway);
    return g?.container === "running" && g.core_ready;
  });
  const wired = slots.find((s) => (s.state === "ready" || s.state === "pending") && s.exit_ip);
  return (
    <>
      <DetailHeader
        badge={<RegionIcon code={n.region} from={n.region_from} icon={landing ? Globe2 : Waypoints} size="detail" />}
        title={n.id}
        note={n.note}
        status={<span className="truncate">{roleText[n.role]} · {n.type}{n.used_by.length ? ` · ${n.used_by.join("、")}在用` : " · 未使用"}</span>}
      />
      <DetailGrid>
        {landing && (
          <ExitCard
            report={n.exit_report}
            currentIP={wired?.exit_ip}
            busy={busy === "检测中"}
            onRefresh={onReport}
            disabledWhy={reachable ? undefined : slots.length ? "查询要经链路发出：先启动一个使用这个落地的槽位" : "查询要经链路发出：先登记并启动一个使用这个落地的槽位"}
          />
        )}
        <section>
          <div className="group-label">连接</div>
          <div className="list-card">
            <KeyValue k="服务器" v={`${n.server}:${n.port}`} />
            <KeyValue k="协议" v={n.type} />
            {n.connect_ipv4 && (
              <KeyValue k="首跳地址" plain>
                <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="selectable font-mono text-[11.5px] text-fg-2">{n.connect_ipv4}</span>
                  {n.fake_ip && <Tag title="本机所在的网络在用 fake-ip 透明代理，系统解析到的是它的映射地址">fake-ip 映射</Tag>}
                  {n.connect_auto && <Tag title="服务器地址是域名，由管理器在本机解析并记下；地址变了可以在菜单里重新解析">自动解析</Tag>}
                </span>
              </KeyValue>
            )}
            {n.source && <KeyValue k="来自订阅" v={`${n.source.sub} · ${n.source.name}`} plain />}
          </div>
        </section>
        {landing && (
          <section>
            <div className="group-label">绑定它的槽位沿用</div>
            <div className="list-card">
              <KeyValue k="时区" plain>
                <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="selectable font-mono text-[11.5px] text-fg-2">{n.tz || "还没有（首次启动槽位时按出口探测）"}</span>
                  {n.tz_fallback && <Tag tone="warn" title="带时区的探测地址都没有应答，暂用兜底值；下次启动槽位会再探测">兜底值</Tag>}
                  {n.tz_auto && !n.tz_fallback && <Tag title="按出口探测到的所在地自动设置">随出口</Tag>}
                </span>
              </KeyValue>
              <KeyValue k="预期出口" v={n.expect_exit_ip || "不检查"} plain={!n.expect_exit_ip} />
              <KeyValue k="槽位" v={slots.length ? slots.map((s) => s.id).join("、") : "没有槽位在用"} plain />
            </div>
          </section>
        )}
      </DetailGrid>
    </>
  );
}

/** 详情栏：一个网关。 */
function GatewayDetail({ g, state, refresh }: { g: Gateway; state: State; refresh: () => Promise<void> }) {
  const running = g.container === "running";
  const transit = state.nodes.find((n) => n.id === g.transit);
  const backups = g.transit_backups ?? [];
  const [changing, setChanging] = useState(false);
  return (
    <>
      <DetailHeader
        badge={<IconBadge icon={Router} />}
        title={g.id}
        note={g.note}
        status={
          <>
            <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", running && g.core_ready ? "bg-ok shadow-[0_0_0_3px_rgb(var(--ok-soft))]" : "bg-faint")} />
            <span>{gatewayStatus(g)}</span>
          </>
        }
        actions={g.transit ? <Button variant="outline" size="sm" onClick={() => setChanging(true)}>更换中转…</Button> : undefined}
      />
      <DetailGrid>
        <section>
          <div className="group-label">转发</div>
          <div className="list-card">
            <KeyValue k="中转" plain>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-fg-2">
                {g.transit ? <span className="selectable font-mono text-[11.5px]">{g.transit}</span> : "不经中转"}
                {!g.transit && <Tag title="各槽位直接连自己的出口节点">不经中转</Tag>}
                {transit && <span className="text-muted">{transit.type} · {transit.server}:{transit.port}</span>}
              </span>
            </KeyValue>
            {backups.length > 0 && (
              <KeyValue k="候选" plain>
                <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-fg-2">
                  <span className="selectable font-mono text-[11.5px]">{backups.join("、")}</span>
                  {g.active_transit && g.active_transit !== g.transit
                    ? <Tag tone="warn" title="主用的中转现在不通，核心已改用候选的；出口不变，主用的恢复后会切回">在用 {g.active_transit}</Tag>
                    : <span className="text-muted">主用的不通时自动改用，出口不变</span>}
                </span>
              </KeyValue>
            )}
            <KeyValue k="配置" v={!running ? "网关没有在运行" : g.config_current ? "与当前的节点和槽位设置一致" : "节点或槽位的设置变过，重新启动网关或槽位后生效"} plain />
            <KeyValue k="槽位" v={g.slots.length ? g.slots.join("、") : "没有槽位"} plain />
          </div>
        </section>
        <section>
          <div className="group-label">网络</div>
          <div className="list-card">
            <KeyValue k="出口网络" v={g.egress_network} />
            <KeyValue k="地址池" v={g.pool} />
            <KeyValue k="槽内解析" v="域名交给落地解析，槽内看到的是 100.64.0.0/10 里的映射地址" plain />
          </div>
        </section>
      </DetailGrid>
      <TransitDialog open={changing} g={g} state={state} onClose={() => setChanging(false)} onChanged={refresh} />
    </>
  );
}

/** 更换一个网关的中转：按点选的顺序，第一个是主用的，其余是候选。 */
function TransitDialog({ open, g, state, onClose, onChanged }: { open: boolean; g: Gateway; state: State; onClose: () => void; onChanged: () => Promise<void> }) {
  const toast = useToast();
  const transits = state.nodes.filter((n) => n.role === "transit");
  const [picked, setPicked] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [skipCheck, setSkipCheck] = useState(false);
  useResetOnOpen(open, () => {
    setPicked([g.transit ?? "", ...(g.transit_backups ?? [])].filter(Boolean));
    setError("");
    setSkipCheck(false);
  });

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const r = (await api.setTransits(g.id, picked, skipCheck)).data;
      const failed = Object.keys(r.failed ?? {});
      if (!r.reloaded) toast(`网关 ${g.id} 的中转已更换，下次启动时生效。`);
      else if (failed.length) toast(`中转已更换，但槽位 ${failed.join("、")} 换完后没有通过核对，排查后重新启动它们。`);
      else toast(`网关 ${g.id} 的中转已更换并生效，重新核对了 ${r.rechecked.length} 个槽位的出口。`);
      await onChanged();
      onClose();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={`更换 ${g.id} 的中转`}
      description="排第一的是主用的，其余是候选：主用的不通时核心自动改用下一个，恢复后切回。换中转不改变出口，出口由各槽位的落地决定。网关在运行时立即生效，并重新核对已就绪槽位的出口。"
      className="max-w-[560px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || picked.length === 0}>{saving ? "更换并核对中…" : "更换"}</Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="list-card">
          {transits.map((n) => {
            const at = picked.indexOf(n.id);
            return (
              <div key={n.id} className="flex items-center gap-3 px-4 py-2">
                <span className="w-14 shrink-0 text-[11.5px] text-muted">{at === 0 ? "主用" : at > 0 ? `候选 ${at}` : ""}</span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px]">{n.id}{n.source && <span className="ml-1.5 text-[11.5px] text-muted">{n.source.name}</span>}</div>
                  <div className="selectable truncate font-mono text-[11px] text-muted">{n.type} · {n.server}:{n.port}</div>
                </div>
                {at > 0 && <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked([n.id, ...picked.filter((x) => x !== n.id)])}>设为主用</Button>}
                {at >= 0
                  ? <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked(picked.filter((x) => x !== n.id))}>移除</Button>
                  : <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked([...picked, n.id])}>{picked.length ? "加为候选" : "设为主用"}</Button>}
              </div>
            );
          })}
        </div>
        <p className="text-[11.5px] leading-[18px] text-muted">
          这里只列已登记的中转节点。要从订阅里挑新的：在订阅的“选节点”里选一个落地测整条链路，把能接通落地的登记为中转。更换时会接上这个网关各槽位的落地逐个验证，有接不通的就不更换。
        </p>
        <div className="flex items-center gap-2.5">
          <Switch checked={skipCheck} onCheckedChange={setSkipCheck} label="不验证能否接通落地" />
          <span className="text-[11.5px] text-muted">不验证能否接通落地（落地只是暂时连不上时用）</span>
        </div>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

/** 详情栏：一个订阅。 */
function SubDetail({ s, state, onBrowse }: { s: Sub; state: State; onBrowse: () => void }) {
  const used = state.nodes.filter((n) => n.source?.sub === s.id);
  return (
    <>
      <DetailHeader
        badge={<IconBadge icon={Rss} />}
        title={s.id}
        note={s.note}
        status={<span className="truncate">订阅 · {s.count} 个节点</span>}
        actions={<Button variant="outline" size="sm" onClick={onBrowse}>选节点…</Button>}
      />
      <DetailGrid>
        <section>
          <div className="group-label">节点</div>
          <div className="list-card">
            <KeyValue k="数量" v={`${s.count} 个`} plain />
            <KeyValue k="延迟" v={s.tested_at ? `${s.usable} 个测通（${relativeTime(s.tested_at)}测）` : "还没测过"} plain />
            <KeyValue k="拉取" v={`${relativeTime(s.fetched_at)}`} plain />
            <KeyValue k="已选用" v={used.length ? used.map((n) => `${n.id}（${n.source!.name}）`).join("、") : "还没有从它里面选用节点"} plain />
          </div>
        </section>
      </DetailGrid>
    </>
  );
}

/** 给新网关挑一个还没被用过的地址池。 */
function freePool(gateways: Gateway[]): string {
  const used = new Set(gateways.map((g) => g.pool));
  for (let i = 250; i >= 200; i--) {
    const pool = `10.213.${i}.0/24`;
    if (!used.has(pool)) return pool;
  }
  return "10.213.250.0/24";
}

function useResetOnOpen(open: boolean, reset: () => void) {
  const [wasOpen, setWasOpen] = useState(false);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) reset();
  }
}

function AddGatewayDialog({ open, state, onClose, onCreated }: { open: boolean; state: State; onClose: () => void; onCreated: (id: string) => void }) {
  const transits = state.nodes.filter((n) => n.role === "transit");
  const [gw, setGw] = useState({ id: "", note: "", transit: "", pool: "" });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useResetOnOpen(open, () => {
    setGw({ id: state.gateways.length ? "" : "main", note: "", transit: transits[0]?.id ?? "", pool: freePool(state.gateways) });
    setError("");
  });

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await api.addGateway({ ...gw, id: gw.id.trim() });
      onCreated(gw.id.trim());
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="新建网关"
      description="一个网关对应一个网关容器，可以接多个槽位，各自从自己的落地出去。带中转时流量先经中转再到落地；不带时各槽位直连自己的落地。"
      className="max-w-[560px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !gw.id.trim()}>{saving ? "登记中…" : "登记"}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="list-card">
          <Line label="名称">
            <Input aria-label="网关名称" value={gw.id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setGw({ ...gw, id: e.target.value })} />
          </Line>
          <Line label="备注">
            <Input aria-label="网关备注" value={gw.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setGw({ ...gw, note: e.target.value })} />
          </Line>
          <Line label="中转">
            <Select aria-label="中转节点" value={gw.transit} onChange={(e) => setGw({ ...gw, transit: e.target.value })}>
              <option value="">不使用中转（各槽位直连自己的落地）</option>
              {transits.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
            </Select>
          </Line>
          <Line label="地址池">
            <Input aria-label="地址池" className="font-mono text-xs" value={gw.pool} onChange={(e) => setGw({ ...gw, pool: e.target.value })} />
          </Line>
          <p className="px-4 pb-3 pl-[116px] text-[11.5px] leading-[18px] text-muted">地址池是网关分给各槽位的私网网段，只在网关和槽位之间使用，一般不用改。</p>
        </div>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

const emptyNode = (): NodeInput => ({ id: "", note: "", role: "landing", type: "socks5", server: "", port: 0, username: "", password: "", tz: "", expect_exit_ip: "" });

function AddNodeDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const [node, setNode] = useState<NodeInput>(emptyNode);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useResetOnOpen(open, () => {
    setNode(emptyNode());
    setError("");
  });

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await api.addNode({ ...node, id: node.id.trim() });
      onCreated(node.id.trim());
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="添加节点"
      description="这里手工填写 SOCKS5 或 HTTP 代理。其他协议的节点从订阅里选，或者用命令行的 node add --file 给出 Mihomo 片段。"
      className="max-w-[560px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !node.id.trim() || !node.server?.trim() || !node.port}>{saving ? "登记中…" : "登记"}</Button>
        </>
      }
    >
      <div className="space-y-5">
        <section>
          <div className="group-label">基础</div>
          <div className="list-card">
            <Line label="名称">
              <Input aria-label="节点名称" value={node.id} maxLength={32} autoFocus placeholder={`${ID_HINT}，例如 home-la`} onChange={(e) => setNode({ ...node, id: e.target.value })} />
            </Line>
            <Line label="备注">
              <Input aria-label="节点备注" value={node.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setNode({ ...node, note: e.target.value })} />
            </Line>
            <Line label="角色">
              <Select aria-label="角色" value={node.role} onChange={(e) => setNode({ ...node, role: e.target.value as Role })}>
                <option value="landing">落地：某个槽位的出口</option>
                <option value="transit">中转：多个槽位共用的第一跳</option>
              </Select>
            </Line>
          </div>
        </section>

        <section>
          <div className="group-label">代理</div>
          <div className="list-card">
            <Line label="类型">
              <Select aria-label="代理类型" value={node.type} onChange={(e) => setNode({ ...node, type: e.target.value })}>
                <option value="socks5">SOCKS5</option>
                <option value="http">HTTP</option>
              </Select>
            </Line>
            <Line label="服务器">
              <Input aria-label="服务器地址" className="font-mono text-xs" placeholder="地址" value={node.server} onChange={(e) => setNode({ ...node, server: e.target.value })} />
              <Input aria-label="端口" className="w-24 font-mono text-xs" type="number" min={1} max={65535} placeholder="端口" value={node.port || ""} onChange={(e) => setNode({ ...node, port: Number(e.target.value) || 0 })} />
            </Line>
            <Line label="账号">
              <Input aria-label="用户名" autoComplete="off" placeholder="用户名（可不填）" value={node.username} onChange={(e) => setNode({ ...node, username: e.target.value })} />
              <Input aria-label="口令" type="password" autoComplete="new-password" placeholder="口令（可不填）" value={node.password} onChange={(e) => setNode({ ...node, password: e.target.value })} />
            </Line>
          </div>
        </section>

        {node.role === "landing" && (
          <section>
            <div className="group-label">出口（可不填）</div>
            <div className="list-card">
              <Line label="时区">
                <Input aria-label="时区" placeholder="例如 America/Los_Angeles；不填则首次启动槽位时按出口所在地设置" value={node.tz} onChange={(e) => setNode({ ...node, tz: e.target.value })} />
              </Line>
              <Line label="预期出口">
                <Input aria-label="预期出口" className="font-mono text-xs" placeholder="出口 IPv4；填了就必须与实测一致，否则不给槽位接线" value={node.expect_exit_ip} onChange={(e) => setNode({ ...node, expect_exit_ip: e.target.value })} />
              </Line>
            </div>
          </section>
        )}

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

function AddSubDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string, count: number) => void }) {
  const [id, setId] = useState("");
  const [note, setNote] = useState("");
  const [url, setUrl] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useResetOnOpen(open, () => {
    setId("");
    setNote("");
    setUrl("");
    setError("");
  });

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const r = await api.addSub(id.trim(), { url: url.trim() }, note.trim());
      onCreated(r.data.id, r.data.count);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="导入订阅"
      description="Clash（Mihomo）格式的订阅地址。地址里通常带有令牌，只保存在这台机器的数据目录里。"
      className="max-w-[560px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !id.trim() || !url.trim()}>{saving ? "读取中…" : "读取订阅"}</Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="list-card">
          <Line label="名称">
            <Input aria-label="订阅名称" value={id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setId(e.target.value)} />
          </Line>
          <Line label="备注">
            <Input aria-label="订阅备注" value={note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setNote(e.target.value)} />
          </Line>
          <Line label="订阅地址">
            <Input aria-label="订阅地址" type="password" autoComplete="off" placeholder="https://…" value={url} onChange={(e) => setUrl(e.target.value)} />
          </Line>
        </div>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

function delayLabel(d: Delay) {
  if (d.delay_ms > 0) return <span className="text-ok">{d.delay_ms} ms</span>;
  if (d.delay_ms < 0) return <span className="text-danger">不通</span>;
  return <span className="text-faint">未测</span>;
}

/** 浏览订阅里的节点：测延迟，再把选中的登记为中转或落地。 */
function SubDialog({ sub, state, onClose, onChanged }: { sub: string | null; state: State; onClose: () => void; onChanged: () => Promise<void> }) {
  const toast = useToast();
  const landings = state.nodes.filter((n) => n.role === "landing");
  const [nodes, setNodes] = useState<Delay[] | null>(null);
  const [via, setVia] = useState("");
  const [testedVia, setTestedVia] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState<Delay | null>(null);
  const [form, setForm] = useState<{ id: string; note: string; role: Role; tz: string }>({ id: "", note: "", role: "transit", tz: "" });
  const [saving, setSaving] = useState(false);
  const [showDead, setShowDead] = useState(false);

  const load = useCallback(async (id: string) => {
    try {
      setNodes(await api.subNodes(id));
    } catch (e) {
      setError(errorText(e));
    }
  }, []);

  useEffect(() => {
    if (!sub) return;
    setNodes(null);
    setError("");
    setPicked(null);
    setTestedVia(null);
    setVia("");
    void load(sub);
  }, [sub, load]);

  const test = async () => {
    if (!sub) return;
    setTesting(true);
    setError("");
    try {
      const r = await api.testSub(sub, via);
      setNodes(r.data);
      setTestedVia(via);
      void onChanged();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setTesting(false);
    }
  };

  const add = async () => {
    if (!sub || !picked) return;
    setSaving(true);
    setError("");
    try {
      await api.addNode({ id: form.id.trim(), note: form.note.trim(), role: form.role, tz: form.role === "landing" ? form.tz.trim() : "", sub, name: picked.name });
      toast(`已登记节点 ${form.id.trim()}（${roleText[form.role]}）。`);
      setPicked(null);
      await onChanged();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  const fastest = nodes?.find((d) => d.delay_ms > 0 && !d.info);
  const dead = nodes?.filter((d) => d.delay_ms < 0).length ?? 0;

  return (
    <Dialog
      open={!!sub}
      onClose={onClose}
      title={`订阅 ${sub ?? ""} 的节点`}
      description="节点自己能用，不代表它肯转发到你的落地。挑中转时选一个落地再测，测的就是“节点 → 落地”的整条链路。"
      className="max-w-[680px] bg-bg"
      footer={<Button variant="outline" onClick={onClose}>关闭</Button>}
    >
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <Select aria-label="测速方式" value={via} disabled={testing} onChange={(e) => setVia(e.target.value)}>
            <option value="">只测节点本身</option>
            {landings.map((n) => <option key={n.id} value={n.id}>接上落地 {n.id} 测整条链路</option>)}
          </Select>
          <Button variant="outline" onClick={test} disabled={testing || !nodes}>
            {testing ? <Loader2 className="animate-spin" /> : <Gauge />}
            {testing ? "测速中…" : "测延迟"}
          </Button>
        </div>
        {testedVia !== null && (
          <p className="text-[11.5px] text-muted">
            {testedVia ? `下面是经节点再到落地 ${testedVia} 的延迟，只在这里显示，不保存。` : "下面是节点自身的延迟，已保存在订阅上。"}
          </p>
        )}

        {picked && (
          <div className="list-card">
            <div className="px-4 py-2.5 text-[12.5px]">
              把 <span className="font-medium">{picked.name}</span> 登记为节点
            </div>
            <Line label="名称">
              <Input aria-label="节点名称" value={form.id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setForm({ ...form, id: e.target.value })} />
            </Line>
            <Line label="备注">
              <Input aria-label="节点备注" value={form.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setForm({ ...form, note: e.target.value })} />
            </Line>
            <Line label="角色">
              <Select aria-label="角色" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value as Role })}>
                <option value="transit">中转：多个槽位共用的第一跳</option>
                <option value="landing">落地：某个槽位的出口</option>
              </Select>
            </Line>
            {form.role === "landing" && (
              <Line label="时区">
                <Input aria-label="时区" placeholder="不填则首次启动槽位时按出口所在地设置" value={form.tz} onChange={(e) => setForm({ ...form, tz: e.target.value })} />
              </Line>
            )}
            <div className="flex justify-end gap-2 px-4 py-2.5">
              <Button variant="outline" size="sm" onClick={() => setPicked(null)}>取消</Button>
              <Button size="sm" onClick={add} disabled={saving || !form.id.trim()}>{saving ? "登记中…" : "登记"}</Button>
            </div>
          </div>
        )}

        {error && <ErrorBox text={error} />}

        {nodes === null && !error && <div className="py-6 text-center text-[12.5px] text-muted">读取中…</div>}
        {nodes && (
          <div className="list-card">
            {nodes.filter((d) => showDead || d.delay_ms >= 0).map((d, i) => (
              <div key={`${d.name}-${i}`} className={cn("flex items-center gap-3 px-4 py-2 hover:bg-card-2", d.info && "opacity-60")}>
                <span className="w-16 shrink-0 text-right font-mono text-[11.5px]">{delayLabel(d)}</span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate text-[13px]">{d.name}</span>
                    {d === fastest && <Tag tone="ok">最快</Tag>}
                    {d.info && <Tag title="订阅里的提示项，不是给人用的线路">提示项</Tag>}
                  </div>
                  <div className="selectable truncate font-mono text-[11px] text-muted">{d.type} · {d.server}</div>
                </div>
                <Button
                  variant={d === fastest ? "default" : "outline"}
                  size="sm"
                  disabled={d.info}
                  onClick={() => {
                    setForm({ id: "", note: "", role: "transit", tz: "" });
                    setPicked(d);
                  }}
                >
                  选用
                </Button>
              </div>
            ))}
            {dead > 0 && (
              <button type="button" className="w-full px-4 py-2 text-left text-[11.5px] text-muted hover:bg-card-2" onClick={() => setShowDead(!showDead)}>
                {showDead ? `收起不通的 ${dead} 个` : `还有 ${dead} 个${testedVia ? `接不通落地 ${testedVia}` : "不通"}，已收起；点这里展开`}
              </button>
            )}
          </div>
        )}
      </div>
    </Dialog>
  );
}
