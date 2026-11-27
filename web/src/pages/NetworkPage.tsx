// 节点与网关：一格一张卡片，点开是这个对象的详情页（落地带出口评估）。
// “卡片网格 + 详情页”的版面和样式取自 Resin（MIT）webui 的 features/platforms/PlatformPage.tsx 与 PlatformDetailPage.tsx，内容是本项目的。
import { Globe2, Loader2, LogOut, Pencil, Play, Plus, Power, RefreshCw, Rss, Router, Trash2, Waypoints } from "lucide-react";
import type React from "react";
import { useState } from "react";
import { ExitCard } from "@/components/ExitCard";
import { RegionIcon } from "@/components/Flag";
import { Field, FormGrid, FormSection, SwitchItem } from "@/components/Form";
import { Menu, type MenuEntry } from "@/components/Menu";
import { NOTE_PLACEHOLDER, NoteDialog, type NoteTarget } from "@/components/NoteDialog";
import { ErrorBox, Page, PageHeader, Tag } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { ApiError, ID_HINT, NOTE_MAX, api, errorText, type Gateway, type NodeInfo, type NodeInput, type Role, type State } from "@/lib/api";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
  /** 去“订阅管理”页：从订阅里挑节点在那边做。 */
  goSubs: () => void;
}

type Failure = { text: string; log: string[] } | null;
type Removing = { kind: "gateway" | "node"; id: string; note: string } | null;
type Selection = { kind: "gateway" | "node"; id: string };

const roleText: Record<Role, string> = { transit: "中转", landing: "落地" };

const gatewayStatus = (g: Gateway) =>
  g.container === "running" ? (g.core_ready ? "运行中" : "核心未就绪") : g.container === "absent" ? "未创建" : g.container === "unknown" ? "状态未知" : "已停止";
const gatewayDot = (g: Gateway) => (g.container !== "running" ? "" : g.core_ready ? "ok" : "warn");

/** 卡片：整张可点，里面的按钮和菜单不算。 */
function tileProps(label: string, onOpen: () => void) {
  return {
    className: "tile",
    role: "button",
    tabIndex: 0,
    "aria-label": label,
    onClick: (e: React.MouseEvent) => !(e.target as HTMLElement).closest("button, [data-menu]") && onOpen(),
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
        e.preventDefault();
        onOpen();
      }
    },
  };
}

function Fact({ k, children }: { k?: string; children: React.ReactNode }) {
  return (
    <span className="platform-fact">
      {k && <span>{k}</span>}
      <strong>{children}</strong>
    </span>
  );
}

function KV({ k, children, mono }: { k: string; children: React.ReactNode; mono?: boolean }) {
  return (
    <div className="kv">
      <span>{k}</span>
      <strong className={mono ? "mono" : "flex flex-wrap items-center gap-1.5"}>{children}</strong>
    </div>
  );
}

export function NetworkPage({ state, loadError, refresh, goSubs }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [dialog, setDialog] = useState<"gateway" | "node" | null>(null);
  const [removing, setRemoving] = useState<Removing>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  /** 打开着详情页的对象；null 时显示卡片网格。 */
  const [open, setOpen] = useState<Selection | null>(null);

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
    if (open?.kind === r.kind && open.id === r.id) setOpen(null);
    const action = r.kind === "gateway" ? () => api.removeGateway(r.id) : () => api.removeNode(r.id);
    run(`${r.kind}:${r.id}`, "删除中…", action, `已删除 ${r.id}`);
  };

  const created = async (text: string) => {
    setDialog(null);
    toast(text);
    await refresh();
  };

  const gatewayActions = (g: Gateway) => ({
    up: () => run(`gateway:${g.id}`, "启动中…", () => api.gatewayUp(g.id), `网关 ${g.id} 已就绪`),
    down: (force: boolean) => run(`gateway:${g.id}`, "停止中…", () => api.gatewayDown(g.id, force), `已停止网关 ${g.id}`),
    note: () => setNoting({ kind: "gateways", what: "网关", id: g.id, note: g.note }),
    remove: () => setRemoving({ kind: "gateway", id: g.id, note: g.slots.length ? `仍有槽位使用该网关（${g.slots.join("、")}），请先删除这些槽位。` : "将删除网关容器及其配置。" }),
  });
  const nodeActions = (n: NodeInfo) => ({
    resolve: () => run(`node:${n.id}`, "解析中…", () => api.resolveNode(n.id), (r) => `${n.id} 的首跳地址：${(r as { data: { connect_ipv4: string } }).data.connect_ipv4}`),
    report: () => run(`node:${n.id}`, "检测中…", () => api.nodeReport(n.id), `已更新 ${n.id} 的出口评估`),
    note: () => setNoting({ kind: "nodes", what: "节点", id: n.id, note: n.note }),
    remove: () => setRemoving({ kind: "node", id: n.id, note: n.used_by.length ? `该节点正被 ${n.used_by.join("、")} 使用，请先解除使用。` : "该节点未被使用。" }),
  });

  const alerts = (
    <>
      {loadError && <ErrorBox text={loadError} />}
      {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}
    </>
  );

  const dialogs = (
    <>
      {state && <AddGatewayDialog open={dialog === "gateway"} state={state} onClose={() => setDialog(null)} onCreated={(id) => created(`已创建网关 ${id}，启动绑定它的槽位时自动启动。`)} />}
      <AddNodeDialog open={dialog === "node"} onClose={() => setDialog(null)} onCreated={(id) => created(`已添加节点 ${id}。`)} />
      <NoteDialog
        target={noting}
        onClose={() => setNoting(null)}
        onSaved={(text) => {
          setNoting(null);
          toast(text);
          void refresh();
        }}
      />
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
    </>
  );

  const openNode = open?.kind === "node" ? state?.nodes.find((n) => n.id === open.id) : undefined;
  const openGateway = open?.kind === "gateway" ? state?.gateways.find((g) => g.id === open.id) : undefined;
  const back = { label: "全部节点与网关", onClick: () => setOpen(null) };

  if (state && openNode) {
    return (
      <>
        <NodeDetail n={openNode} state={state} busy={busy[`node:${openNode.id}`]} a={nodeActions(openNode)} alerts={alerts} back={back} />
        {dialogs}
      </>
    );
  }
  if (state && openGateway) {
    return (
      <>
        <GatewayDetail g={openGateway} state={state} busy={busy[`gateway:${openGateway.id}`]} a={gatewayActions(openGateway)} alerts={alerts} back={back} refresh={refresh} />
        {dialogs}
      </>
    );
  }

  const empty = !!state && !state.gateways.length && !state.nodes.length;

  return (
    <Page className="platform-page">
      <PageHeader
        title="节点与网关"
        subtitle="管理网关、中转与落地节点。"
        actions={
          <>
            <Button variant="outline" onClick={goSubs} title="前往“订阅管理”页，从订阅的节点中选择"><Rss />从订阅中选择</Button>
            <Button variant="outline" onClick={() => setDialog("node")}><Plus />添加节点</Button>
            <Button onClick={() => setDialog("gateway")} disabled={!state}><Plus />新建网关</Button>
          </>
        }
      />
      {alerts}

      {!state && !loadError && <Card className="platform-cards-container"><p className="muted">加载中…</p></Card>}

      {empty && (
        <Card className="platform-cards-container">
          <div className="empty-box plain flex-col !gap-3 !py-12">
            <Globe2 size={20} />
            <p className="text-[15px] font-semibold text-fg">暂无节点</p>
            <p className="max-w-[480px] text-center text-[13px]">请先添加落地节点（槽位的出口）；需要中转时，在“订阅管理”页添加订阅，并按延迟选择节点。</p>
            <Button onClick={() => setDialog("node")}>添加节点</Button>
          </div>
        </Card>
      )}

      {!!state?.gateways.length && (
        <Card className="platform-cards-container">
          <div className="platform-drawer-section-head mb-3">
            <h4>网关</h4>
            <p>将槽位的流量转发到各自的落地。</p>
          </div>
          <div className="tile-grid">
            {state.gateways.map((g) => <GatewayTile key={g.id} g={g} busy={busy[`gateway:${g.id}`]} a={gatewayActions(g)} onOpen={() => setOpen({ kind: "gateway", id: g.id })} />)}
          </div>
        </Card>
      )}

      {!!state?.nodes.length && (
        <Card className="platform-cards-container">
          <div className="platform-drawer-section-head mb-3">
            <h4>节点</h4>
            <p>落地是槽位的出口，中转是多个槽位共用的第一跳。</p>
          </div>
          <div className="tile-grid">
            {state.nodes.map((n) => <NodeTile key={n.id} n={n} busy={busy[`node:${n.id}`]} a={nodeActions(n)} onOpen={() => setOpen({ kind: "node", id: n.id })} />)}
          </div>
        </Card>
      )}

      {dialogs}
    </Page>
  );
}

type GatewayActions = { up: () => void; down: (force: boolean) => void; note: () => void; remove: () => void };
type NodeActions = { resolve: () => void; report: () => void; note: () => void; remove: () => void };

function gatewayMenu(g: Gateway, a: GatewayActions): (MenuEntry | "sep")[] {
  return [
    { label: "重新应用配置", icon: RefreshCw, onSelect: a.up },
    { label: "强制停止（槽位将断网）", icon: LogOut, onSelect: () => a.down(true), disabled: g.container !== "running" },
    { label: "备注…", icon: Pencil, onSelect: a.note },
    "sep",
    { label: "删除…", icon: Trash2, danger: true, onSelect: a.remove },
  ];
}

function nodeMenu(n: NodeInfo, a: NodeActions): (MenuEntry | "sep")[] {
  return [
    { label: "重新解析首跳地址", icon: RefreshCw, onSelect: a.resolve, disabled: !n.connect_auto },
    { label: "备注…", icon: Pencil, onSelect: a.note },
    "sep",
    { label: "删除…", icon: Trash2, danger: true, onSelect: a.remove },
  ];
}

function GatewayBadges({ g }: { g: Gateway }) {
  const running = g.container === "running";
  return (
    <>
      {running && !g.core_ready && <Badge variant="warning">核心未就绪</Badge>}
      {running && g.core_ready && !g.config_current && <Badge variant="warning" title="节点或槽位的设置有改动，重新启动网关或槽位后生效">配置待更新</Badge>}
      {g.active_transit && g.active_transit !== g.transit && <Badge variant="warning" title="主用中转当前不可用，核心已切换到备用中转；出口不变，主用恢复后自动切回">当前使用 {g.active_transit}</Badge>}
    </>
  );
}

function GatewayToggle({ g, a, busy, size = "sm" }: { g: Gateway; a: GatewayActions; busy?: string; size?: "sm" | "default" }) {
  if (busy) return <Button variant="outline" size={size} disabled><Loader2 className="spin" />{busy}</Button>;
  if (g.container === "running") return <Button variant="outline" size={size} onClick={() => a.down(false)}><Power />停止</Button>;
  return <Button variant={size === "sm" ? "success" : "default"} size={size} onClick={a.up} disabled={g.container === "unknown"}><Play />启动</Button>;
}

function GatewayTile({ g, busy, a, onOpen }: { g: Gateway; busy?: string; a: GatewayActions; onOpen: () => void }) {
  return (
    <div {...tileProps(`网关 ${g.id}，打开详情`, onOpen)}>
      <div className="tile-head">
        <RegionIcon icon={Router} />
        <div className="tile-title">
          <p title={g.note}>{g.id}{g.note && <span className="ml-2 font-sans text-[12px] font-normal text-muted">{g.note}</span>}</p>
          <p><span className={`status-dot mr-1.5 inline-block align-middle ${gatewayDot(g)}`} />{gatewayStatus(g)}</p>
        </div>
        <Menu items={gatewayMenu(g, a)} disabled={!!busy} />
      </div>
      <div className="platform-tile-facts">
        {g.transit ? <Fact k="中转">{g.transit}</Fact> : <Fact>不经中转</Fact>}
        {!!g.transit_backups?.length && <Fact k="备用">{g.transit_backups.length} 个</Fact>}
        <Fact k="地址池">{g.pool}</Fact>
        <GatewayBadges g={g} />
      </div>
      <div className="platform-tile-foot">
        <span className="min-w-0 flex-1 truncate">{g.slots.length ? `槽位 ${g.slots.join("、")}` : "无槽位"}</span>
        <GatewayToggle g={g} a={a} busy={busy} />
      </div>
    </div>
  );
}

function NodeTile({ n, busy, a, onOpen }: { n: NodeInfo; busy?: string; a: NodeActions; onOpen: () => void }) {
  const r = n.exit_report;
  const where = r ? [r.country_code || r.country, r.city].filter(Boolean).join(" ") : "";
  return (
    <div {...tileProps(`节点 ${n.id}，打开详情`, onOpen)}>
      <div className="tile-head">
        <RegionIcon code={n.region} from={n.region_from} icon={n.role === "landing" ? Globe2 : Waypoints} />
        <div className="tile-title">
          <p title={n.note}>{n.id}{n.note && <span className="ml-2 font-sans text-[12px] font-normal text-muted">{n.note}</span>}</p>
          <p className="mono">{n.type} · {n.server}:{n.port}</p>
        </div>
        <Menu items={nodeMenu(n, a)} disabled={!!busy} />
      </div>
      <div className="platform-tile-facts">
        <Badge variant={n.role === "landing" ? "info" : "neutral"}>{roleText[n.role]}</Badge>
        {n.source && <Badge variant="neutral" title={`取自订阅 ${n.source.sub}`} className="max-w-[180px] truncate">{n.source.name}</Badge>}
        {r?.trust_score !== undefined && <Fact k="信任分">{r.trust_score}</Fact>}
        {r && (r.flags.length ? <Badge variant="warning" title={r.flags.map((f) => f.name).join("、")}>{r.flags.length} 个不利标记</Badge> : <Badge variant="success">无不利标记</Badge>)}
        {n.tz_fallback && <Badge variant="warning" title="未探测到出口所在的时区，暂用默认值">默认时区</Badge>}
      </div>
      <div className="platform-tile-foot">
        <span className="min-w-0 flex-1 truncate">
          {r ? <><span className="mono font-semibold text-fg">{r.ip}</span>{where && ` · ${where}`}</> : n.connect_ipv4 ? <>首跳 <span className="mono">{n.connect_ipv4}</span></> : n.role === "landing" ? "暂无出口评估" : "中转"}
        </span>
        {busy ? <span className="flex items-center gap-1.5"><Loader2 size={14} className="spin" />{busy}</span> : <span className="max-w-[45%] truncate" title={n.used_by.join("、")}>{n.used_by.length ? `${n.used_by.join("、")} 使用中` : "未使用"}</span>}
      </div>
    </div>
  );
}

interface DetailCommon {
  state: State;
  busy?: string;
  alerts: React.ReactNode;
  back: { label: string; onClick: () => void };
}

/** 详情页：一个节点。落地显示出口评估，中转只有连接信息。 */
function NodeDetail({ n, state, busy, a, alerts, back }: DetailCommon & { n: NodeInfo; a: NodeActions }) {
  const landing = n.role === "landing";
  const slots = state.slots.filter((s) => s.landing === n.id);
  // 出口评估要经链路查询：得有一个用着这个落地的槽位，它的网关在运行。
  const reachable = slots.some((s) => {
    const g = state.gateways.find((x) => x.id === s.gateway);
    return g?.container === "running" && g.core_ready;
  });
  const wired = slots.find((s) => (s.state === "ready" || s.state === "pending") && s.exit_ip);
  return (
    <Page className="platform-page platform-detail-page">
      <PageHeader
        back={back}
        title={n.id}
        note={n.note}
        subtitle={landing ? "出口评估与连接信息。" : "连接信息。"}
        actions={
          <>
            {busy && <Button variant="outline" disabled><Loader2 className="spin" />{busy}</Button>}
            <Menu items={nodeMenu(n, a)} disabled={!!busy} />
          </>
        }
      />
      {alerts}

      <Card className="platform-directory-card platform-detail-header-card">
        <div className="platform-detail-header-main">
          <div className="flex min-w-0 items-center gap-3">
            <RegionIcon code={n.region} from={n.region_from} icon={landing ? Globe2 : Waypoints} size="detail" />
            <div className="min-w-0">
              <h3 className="truncate">{n.exit_report ? <span className="mono">{n.exit_report.ip}</span> : n.id}</h3>
              <p>{roleText[n.role]} · {n.type} · {n.used_by.length ? `${n.used_by.join("、")} 使用中` : "未使用"}</p>
            </div>
          </div>
          <div className="platform-detail-header-meta">
            <div className="flex flex-wrap justify-end gap-1.5">
              <Badge variant={landing ? "info" : "neutral"}>{roleText[n.role]}</Badge>
              {n.direct && <Badge variant="warning" title="直接使用订阅中的节点作为出口：出口地址为多人共用">直出</Badge>}
              {n.tz_fallback && <Badge variant="warning" title="带时区信息的探测地址均无响应，暂用默认值；下次启动槽位时重新探测">默认时区</Badge>}
            </div>
          </div>
        </div>
        <div className="platform-detail-header-footer">
          <div className="platform-tile-facts">
            <Fact k="服务器">{n.server}:{n.port}</Fact>
            <Fact k="协议">{n.type}</Fact>
            {n.connect_ipv4 && <Fact k="首跳">{n.connect_ipv4}</Fact>}
            {n.source && <Fact k="来自订阅">{n.source.sub}</Fact>}
          </div>
        </div>
      </Card>

      <div className={landing ? "grid items-stretch gap-3.5 min-[1100px]:grid-cols-2" : "grid gap-3.5"}>
        {landing && (
          <ExitCard
            report={n.exit_report}
            currentIP={wired?.exit_ip}
            busy={busy === "检测中…"}
            onRefresh={a.report}
            disabledWhy={reachable ? undefined : slots.length ? "查询需经链路发出：请先启动使用该落地的槽位" : "查询需经链路发出：请先创建并启动使用该落地的槽位"}
          />
        )}

        <Card className="panel">
          <div className="panel-head">
            <div>
              <h3>连接</h3>
              <p>节点的连接参数</p>
            </div>
          </div>
          <div className="kv-grid">
            <KV k="服务器" mono>{n.server}:{n.port}</KV>
            <KV k="协议" mono>{n.type}</KV>
            {n.connect_ipv4 && (
              <KV k="首跳地址">
                <span className="mono">{n.connect_ipv4}</span>
                {n.fake_ip && <Tag title="本机所在网络使用 fake-ip 透明代理，系统解析得到的是映射地址">fake-ip 映射</Tag>}
                {n.connect_auto && <Tag title="服务器地址为域名，由管理器在本机解析并保存；地址变更后可在菜单中重新解析">自动解析</Tag>}
              </KV>
            )}
            {n.source && <KV k="来自订阅">{n.source.sub} · {n.source.name}</KV>}
          </div>

          {landing && (
            <>
              <div className="platform-drawer-section-head mt-1">
                <h4>绑定该落地的槽位沿用</h4>
                <p>时区与预期出口。</p>
              </div>
              <div className="kv-grid">
                <KV k="时区">
                  <span className="mono">{n.tz || "未设置（首次启动槽位时按出口探测）"}</span>
                  {n.tz_fallback && <Tag tone="warn" title="带时区信息的探测地址均无响应，暂用默认值；下次启动槽位时重新探测">默认值</Tag>}
                  {n.tz_auto && !n.tz_fallback && <Tag title="按探测到的出口所在地自动设置">随出口</Tag>}
                </KV>
                <KV k="预期出口" mono={!!n.expect_exit_ip}>{n.expect_exit_ip || "不检查"}</KV>
                <KV k="槽位">{slots.length ? slots.map((s) => s.id).join("、") : "无槽位使用"}</KV>
              </div>
            </>
          )}
        </Card>
      </div>
    </Page>
  );
}

/** 详情页：一个网关。 */
function GatewayDetail({ g, state, busy, a, alerts, back, refresh }: DetailCommon & { g: Gateway; a: GatewayActions; refresh: () => Promise<void> }) {
  const running = g.container === "running";
  const transit = state.nodes.find((n) => n.id === g.transit);
  const backups = g.transit_backups ?? [];
  const [changing, setChanging] = useState(false);
  return (
    <Page className="platform-page platform-detail-page">
      <PageHeader
        back={back}
        title={g.id}
        note={g.note}
        subtitle="转发、网络与槽位。"
        actions={
          <>
            {g.transit && <Button variant="outline" onClick={() => setChanging(true)}><Waypoints />更换中转</Button>}
            <GatewayToggle g={g} a={a} busy={busy} size="default" />
            <Menu items={gatewayMenu(g, a)} disabled={!!busy} />
          </>
        }
      />
      {alerts}

      <Card className="platform-directory-card platform-detail-header-card">
        <div className="platform-detail-header-main">
          <div className="flex min-w-0 items-center gap-3">
            <RegionIcon icon={Router} size="detail" />
            <div className="min-w-0">
              <h3 className="truncate">{g.id}</h3>
              <p><span className={`status-dot mr-1.5 inline-block align-middle ${gatewayDot(g)}`} />{gatewayStatus(g)} · {g.slots.length ? `${g.slots.length} 个槽位` : "无槽位"}</p>
            </div>
          </div>
          <div className="platform-detail-header-meta">
            <div className="flex flex-wrap justify-end gap-1.5">
              {running && g.core_ready && g.config_current && <Badge variant="success">配置已生效</Badge>}
              <GatewayBadges g={g} />
            </div>
          </div>
        </div>
        <div className="platform-detail-header-footer">
          <div className="platform-tile-facts">
            {g.transit ? <Fact k="中转">{g.transit}</Fact> : <Fact>不经中转</Fact>}
            <Fact k="出口网络">{g.egress_network}</Fact>
            <Fact k="地址池">{g.pool}</Fact>
          </div>
        </div>
      </Card>

      <div className="grid items-stretch gap-3.5 min-[1100px]:grid-cols-2">
        <Card className="panel">
          <div className="panel-head">
            <div>
              <h3>转发</h3>
              <p>中转与所接的槽位</p>
            </div>
          </div>
          <div className="kv-grid">
            <KV k="中转">
              {g.transit ? <span className="mono">{g.transit}</span> : "不经中转，各槽位直接连接自己的出口节点"}
              {transit && <span className="text-[12px] font-normal text-muted">{transit.type} · {transit.server}:{transit.port}</span>}
            </KV>
            {backups.length > 0 && (
              <KV k="备用">
                <span className="mono">{backups.join("、")}</span>
                {g.active_transit && g.active_transit !== g.transit
                  ? <Tag tone="warn" title="主用中转当前不可用，核心已切换到备用中转；出口不变，主用恢复后自动切回">当前使用 {g.active_transit}</Tag>
                  : <span className="text-[12px] font-normal text-muted">主用不可用时自动切换，出口不变</span>}
              </KV>
            )}
            <KV k="配置">{!running ? "网关未运行" : g.config_current ? "与当前的节点和槽位设置一致" : "节点或槽位的设置有改动，重新启动网关或槽位后生效"}</KV>
            <KV k="槽位">{g.slots.length ? g.slots.join("、") : "无槽位"}</KV>
          </div>
        </Card>

        <Card className="panel">
          <div className="panel-head">
            <div>
              <h3>网络</h3>
              <p>出口网络与地址池</p>
            </div>
          </div>
          <div className="kv-grid">
            <KV k="出口网络" mono>{g.egress_network}</KV>
            <KV k="地址池" mono>{g.pool}</KV>
            <KV k="槽内解析">域名由落地解析，槽内得到的是 100.64.0.0/10 中的映射地址</KV>
          </div>
        </Card>
      </div>

      <TransitDialog open={changing} g={g} state={state} onClose={() => setChanging(false)} onChanged={refresh} />
    </Page>
  );
}

/** 更换一个网关的中转：按点选的顺序，第一个是主用的，其余是备用。 */
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
      else if (failed.length) toast(`中转已更换，但槽位 ${failed.join("、")} 更换后未通过出口核对，请排查后重新启动。`);
      else toast(`网关 ${g.id} 的中转已更换并生效，已重新核对 ${r.rechecked.length} 个槽位的出口。`);
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
      busy={saving}
      form
      description="排在首位的为主用，其余为备用：主用不可用时核心自动切换到下一个，恢复后切回。更换中转不改变出口，出口由各槽位的落地决定。网关运行时立即生效，并重新核对已接线槽位的出口。"
      className="mid"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || picked.length === 0}>{saving ? "更换中…" : "更换"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-2">
          {transits.map((n) => {
            const at = picked.indexOf(n.id);
            return (
              <div key={n.id} className="snapshot-row">
                <span className="w-12 shrink-0 text-[12px] font-semibold text-brand">{at === 0 ? "主用" : at > 0 ? `备用 ${at}` : ""}</span>
                <div className="grow">
                  <p>{n.id}{n.source && <span className="ml-1.5 text-[12px] font-normal text-muted">{n.source.name}</span>}</p>
                  <p className="mono">{n.type} · {n.server}:{n.port}</p>
                </div>
                {at > 0 && <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked([n.id, ...picked.filter((x) => x !== n.id)])}>设为主用</Button>}
                {at >= 0
                  ? <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked(picked.filter((x) => x !== n.id))}>移除</Button>
                  : <Button variant="outline" size="sm" disabled={saving} onClick={() => setPicked([...picked, n.id])}>{picked.length ? "设为备用" : "设为主用"}</Button>}
              </div>
            );
          })}
        </div>
        <p className="field-hint !mt-0">
          此处仅列出已添加的中转节点。如需从订阅中选择：在“订阅管理”页选择一个落地测试整条链路，将可连通该落地的节点添加为中转。更换时逐个验证该网关各槽位的落地能否连通，任一失败则不更换。
        </p>
        <SwitchItem label="跳过落地连通性验证" hint="仅在落地暂时无法连接时使用。" checked={skipCheck} onChange={setSkipCheck} />
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
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
      busy={saving}
      form
      description="一个网关对应一个网关容器，可接入多个槽位，各槽位经自己的落地出站。使用中转时流量先经中转再到落地；不使用时各槽位直连自己的落地。"
      className="mid"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !gw.id.trim()}>{saving ? "创建中…" : "创建"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <FormGrid>
          <Field label="名称">
            <Input aria-label="网关名称" value={gw.id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setGw({ ...gw, id: e.target.value })} />
          </Field>
          <Field label="备注">
            <Input aria-label="网关备注" value={gw.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setGw({ ...gw, note: e.target.value })} />
          </Field>
          <Field label="中转" wide>
            <Select aria-label="中转节点" value={gw.transit} onChange={(e) => setGw({ ...gw, transit: e.target.value })}>
              <option value="">不使用中转（各槽位直连自己的落地）</option>
              {transits.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
            </Select>
          </Field>
          <Field label="地址池" wide hint="网关分配给各槽位的私有网段，仅在网关与槽位之间使用，通常无需修改。">
            <Input aria-label="地址池" className="mono" value={gw.pool} onChange={(e) => setGw({ ...gw, pool: e.target.value })} />
          </Field>
        </FormGrid>
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
      busy={saving}
      form
      description="此处手动填写 SOCKS5 或 HTTP 代理。其他协议的节点请从订阅中选择，或通过命令行 node add --file 提供 Mihomo 配置片段。"
      className="mid"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !node.id.trim() || !node.server?.trim() || !node.port}>{saving ? "添加中…" : "添加"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <FormSection title="基础">
          <FormGrid>
            <Field label="名称">
              <Input aria-label="节点名称" value={node.id} maxLength={32} autoFocus placeholder={`${ID_HINT}，例如 home-la`} onChange={(e) => setNode({ ...node, id: e.target.value })} />
            </Field>
            <Field label="备注">
              <Input aria-label="节点备注" value={node.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setNode({ ...node, note: e.target.value })} />
            </Field>
            <Field label="角色" wide>
              <Select aria-label="角色" value={node.role} onChange={(e) => setNode({ ...node, role: e.target.value as Role })}>
                <option value="landing">落地：某个槽位的出口</option>
                <option value="transit">中转：多个槽位共用的第一跳</option>
              </Select>
            </Field>
          </FormGrid>
        </FormSection>

        <FormSection title="代理">
          <FormGrid>
            <Field label="类型" wide>
              <Select aria-label="代理类型" value={node.type} onChange={(e) => setNode({ ...node, type: e.target.value })}>
                <option value="socks5">SOCKS5</option>
                <option value="http">HTTP</option>
              </Select>
            </Field>
            <Field label="服务器">
              <Input aria-label="服务器地址" className="mono" placeholder="地址" value={node.server} onChange={(e) => setNode({ ...node, server: e.target.value })} />
            </Field>
            <Field label="端口">
              <Input aria-label="端口" className="mono" type="number" min={1} max={65535} placeholder="1–65535" value={node.port || ""} onChange={(e) => setNode({ ...node, port: Number(e.target.value) || 0 })} />
            </Field>
            <Field label="用户名">
              <Input aria-label="用户名" autoComplete="off" placeholder="选填" value={node.username} onChange={(e) => setNode({ ...node, username: e.target.value })} />
            </Field>
            <Field label="密码">
              <Input aria-label="密码" type="password" autoComplete="new-password" placeholder="选填" value={node.password} onChange={(e) => setNode({ ...node, password: e.target.value })} />
            </Field>
          </FormGrid>
        </FormSection>

        {node.role === "landing" && (
          <FormSection title="出口" hint="均为选填。">
            <FormGrid>
              <Field label="时区" hint="留空则首次启动槽位时按出口所在地设置。">
                <Input aria-label="时区" placeholder="例如 America/Los_Angeles" value={node.tz} onChange={(e) => setNode({ ...node, tz: e.target.value })} />
              </Field>
              <Field label="预期出口" hint="填写后必须与实测出口一致，否则不为槽位接线。">
                <Input aria-label="预期出口" className="mono" placeholder="出口 IPv4" value={node.expect_exit_ip} onChange={(e) => setNode({ ...node, expect_exit_ip: e.target.value })} />
              </Field>
            </FormGrid>
          </FormSection>
        )}

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
