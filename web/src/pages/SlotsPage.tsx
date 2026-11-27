// 槽位：一格一张卡片，点开是带页签的详情页（概况、流量与连接、环境与挂载、运维）。
// “卡片网格 + 详情页”的版面和样式取自 Resin（MIT）webui 的 features/platforms/PlatformPage.tsx 与 PlatformDetailPage.tsx，内容是本项目的。
import { ArrowRight, Ban, Box, Loader2, Pencil, Play, Plus, Power, RefreshCw, RotateCw, ShieldCheck, Sparkles, Terminal, Trash2 } from "lucide-react";
import type React from "react";
import { useEffect, useRef, useState } from "react";
import { ActionNotice, useActions } from "@/components/Actions";
import { CopyButton } from "@/components/CopyButton";
import { ExitCard } from "@/components/ExitCard";
import { RegionIcon } from "@/components/Flag";
import { Field, FormGrid, FormSection, SwitchItem } from "@/components/Form";
import { Menu, type MenuEntry } from "@/components/Menu";
import { NOTE_PLACEHOLDER, NoteDialog, type NoteTarget } from "@/components/NoteDialog";
import { ErrorBox, Page, PageHeader, Tabs, Tag } from "@/components/PageHeader";
import { SlotTraffic } from "@/components/SlotTraffic";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { ID_HINT, NOTE_MAX, api, errorText, type Delay, type History, type Slot, type SlotInput, type SlotLive, type SlotState, type State } from "@/lib/api";
import { usePolling } from "@/lib/live";
import { formatBytes, rateText, relativeTime } from "@/lib/utils";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
  goNetwork: () => void;
  goSubs: () => void;
  /** 打开着详情页的槽位；null 时显示卡片网格。 */
  open: string | null;
  setOpen: (id: string | null) => void;
}

export const stateText: Record<SlotState, string> = {
  ready: "已接线",
  pending: "已接线 · 配置待应用",
  offline: "运行中 · 未接线",
  stopped: "已停止",
  absent: "未创建",
  hold: "手动断网",
  unknown: "状态未知",
};

/** 还不能新建槽位时的说明：槽位需要一个出口。 */
const NEED_EXIT = "请先在“节点与网关”页添加落地节点与网关，或在“订阅管理”页添加订阅";

const isWired = (s: Slot) => s.state === "ready" || s.state === "pending";
const isRunning = (s: Slot) => isWired(s) || s.state === "offline";
const dotTone = (s: Slot) => (isWired(s) ? "ok" : s.state === "hold" ? "bad" : s.state === "offline" ? "warn" : "");

interface Actions {
  up: (s: Slot, recreate?: boolean) => void;
  stop: (s: Slot) => void;
  hold: (s: Slot) => void;
  release: (s: Slot) => void;
  enter: (s: Slot) => void;
  note: (s: Slot) => void;
  remove: (s: Slot) => void;
  report: (s: Slot) => void;
}

function menuItems(s: Slot, a: Actions): (MenuEntry | "sep")[] {
  return [
    { label: "进入命令…", icon: Terminal, onSelect: () => a.enter(s) },
    { label: "重新探测并接线", icon: RefreshCw, onSelect: () => a.up(s), disabled: s.state === "hold" },
    { label: "重建 worker 并接线", icon: RotateCw, onSelect: () => a.up(s, true), disabled: s.state === "hold" },
    s.state === "hold" ? { label: "解除手动断网", icon: ShieldCheck, onSelect: () => a.release(s) } : { label: "手动断网", icon: Ban, onSelect: () => a.hold(s) },
    { label: "备注…", icon: Pencil, onSelect: () => a.note(s) },
    "sep",
    { label: "删除…", icon: Trash2, onSelect: () => a.remove(s), danger: true },
  ];
}

/** 主要的那个按钮：在运行就是“停止”，被断网就是“解除”，否则是“启动”。 */
function MainAction({ s, a, busy, size = "sm" }: { s: Slot; a: Actions; busy?: string; size?: "sm" | "default" }) {
  if (busy) {
    return (
      <Button variant="outline" size={size} disabled>
        <Loader2 className="spin" />
        {busy}
      </Button>
    );
  }
  if (isRunning(s)) return <Button variant="outline" size={size} onClick={() => a.stop(s)}><Power />停止</Button>;
  if (s.state === "hold") return <Button variant="outline" size={size} onClick={() => a.release(s)}><ShieldCheck />解除断网</Button>;
  return <Button variant={size === "sm" ? "success" : "default"} size={size} onClick={() => a.up(s)} disabled={s.state === "unknown"}><Play />启动</Button>;
}

export function SlotsPage({ state, loadError, refresh, goNetwork, goSubs, open, setOpen }: Props) {
  const toast = useToast();
  const { busy: allBusy, run: runAction, setNotice, confirm } = useActions();
  const opened = useRef(open);
  opened.current = open;
  const busy = (id: string): string | undefined => allBusy[`slot:${id}`];
  const run = <T,>(s: Slot, label: string, action: () => Promise<T>, done?: string | ((r: T) => string)) => runAction(`slot:${s.id}`, label, action, done);
  const [adding, setAdding] = useState(false);
  const [entering, setEntering] = useState<Slot | null>(null);
  const [deleting, setDeleting] = useState<Slot | null>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  const [purge, setPurge] = useState(false);
  // 各槽位此刻的流量和最近 24 小时的趋势。
  const traffic = usePolling(api.traffic, 5000);
  const history = usePolling(() => api.history(24), 60_000);

  const actions: Actions = {
    up: (s, recreate = false) => {
      const up = () => run(s, recreate ? "重建中…" : "启动中…", () => api.slotUp(s.id, recreate), (r) => `${s.id} 已接线，出口 ${r.data.exit}`);
      if (!recreate) return void up();
      confirm({ title: `重建槽位 ${s.id} 的 worker？`, text: "将删除并重新创建 worker 容器，槽内正在运行的程序会中断。HOME 与身份保留。", action: "重建", onConfirm: up });
    },
    stop: (s) => confirm({ title: `停止槽位 ${s.id}？`, text: "将停止 worker 容器并断开接线，槽内正在运行的程序会中断。", action: "停止", onConfirm: () => run(s, "停止中…", () => api.slotStop(s.id), `已停止 ${s.id}`) }),
    hold: (s) => run(s, "断网中…", () => api.slotHold(s.id, "通过网页界面手动断网"), `已断开 ${s.id} 的网络`),
    release: (s) => run(s, "解除中…", () => api.slotRelease(s.id), `已解除 ${s.id} 的手动断网，启动后恢复网络`),
    enter: setEntering,
    note: (s) => setNoting({ kind: "slots", what: "槽位", id: s.id, note: s.note }),
    remove: (s) => {
      setPurge(false);
      setDeleting(s);
    },
    report: (s) => run(s, "检测中…", () => api.nodeReport(s.landing), `已更新 ${s.landing} 的出口评估`),
  };

  const slots = state?.slots ?? null;
  // 有网关和落地，或者有订阅（可以直出）就能新建。
  const canAdd = !!state && ((state.gateways.length > 0 && state.nodes.some((n) => n.role === "landing")) || state.subs.length > 0);
  const current = open ? slots?.find((s) => s.id === open) : undefined;

  const alerts = (
    <>
      {loadError && <ErrorBox text={loadError} />}
      {state?.docker_error && <ErrorBox text={`无法连接 Docker，以下仅显示配置，状态未知：${state.docker_error}`} />}
      <ActionNotice />
    </>
  );

  const dialogs = (
    <>
      {state && (
        <AddSlotDialog
          open={adding}
          state={state}
          onClose={() => setAdding(false)}
          onCreated={async (id, warnings) => {
            setAdding(false);
            await refresh();
            toast(`已创建槽位 ${id}，点击“启动”接线。`);
            // 创建时的提醒需要读完并处理，留在页面上，不放进短暂的提示条。
            if (warnings.length) setNotice({ tone: "warning", text: warnings.join("\n") });
          }}
        />
      )}

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
        open={!!entering}
        onClose={() => setEntering(null)}
        title={`进入 ${entering?.id ?? ""}`}
        description="网页界面不提供终端。请在本机终端执行以下命令；在项目目录中执行时，若该目录未挂载，会询问是否挂载。"
        className="mid"
        footer={
          <>
            <Button variant="outline" onClick={() => setEntering(null)}>关闭</Button>
            {entering && <CopyButton text={entering.enter_command} label="复制命令" />}
          </>
        }
      >
        <code className="code-line">{entering?.enter_command}</code>
        <p className="field-hint mt-2">将 enter 替换为 claude 可直接启动 Claude Code（首次使用时会提示在槽内安装）。</p>
        {!!entering?.mounts.length && (
          <div className="mt-4">
            <div className="field-label mb-1.5">已挂载的目录</div>
            <div className="mono flex flex-col gap-1 text-[12.5px] text-fg-2">
              {entering.mounts.map((m) => <div key={m.target} className="break-all">{m.source === m.target ? m.source : `${m.source} → ${m.target}`}{m.read_only ? "（只读）" : ""}</div>)}
            </div>
          </div>
        )}
      </Dialog>

      <Dialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={`删除槽位 ${deleting?.id ?? ""}？`}
        description="将删除 worker 容器并断开接线。默认保留 HOME 与身份，以同一名称重新创建槽位时沿用。"
        footer={
          <>
            <Button variant="outline" onClick={() => setDeleting(null)}>取消</Button>
            <Button
              variant="destructive"
              onClick={() => {
                const s = deleting!;
                setDeleting(null);
                // 删除成功后才离开详情页；失败时留在原处，原因显示在页面上。
                void run(s, "删除中…", () => api.removeSlot(s.id, purge), `已删除 ${s.id}`).then((ok) => ok && opened.current === s.id && setOpen(null));
              }}
            >
              删除
            </Button>
          </>
        }
      >
        <SwitchItem label="同时删除 HOME 与身份" hint="槽内的登录状态与文件将丢失，无法恢复。" checked={purge} onChange={setPurge} />
      </Dialog>
    </>
  );

  if (current && state) {
    return (
      <>
        <SlotDetail s={current} state={state} live={traffic?.slots[current.id]} history={history} busy={busy(current.id)} a={actions} alerts={alerts} onBack={() => setOpen(null)} />
        {dialogs}
      </>
    );
  }

  return (
    <Page className="platform-page">
      <PageHeader
        title="槽位"
        subtitle="管理槽位，查看状态与出口。"
        actions={
          <Button onClick={() => setAdding(true)} disabled={!canAdd} title={canAdd ? undefined : NEED_EXIT}>
            <Plus />
            新建槽位
          </Button>
        }
      />
      {alerts}

      <Card className="platform-cards-container">
        {slots === null && !loadError && <p className="muted">正在加载槽位…</p>}

        {slots && slots.length === 0 && (
          <div className="empty-box plain flex-col !gap-3 !py-12">
            <Sparkles size={20} />
            <p className="text-[15px] font-semibold text-fg">暂无槽位</p>
            <p className="max-w-[460px] text-center text-[13px]">{canAdd ? "新建槽位时选择出口（自己的落地，或订阅中的节点），并指定要挂载的项目目录。" : `${NEED_EXIT}。`}</p>
            {canAdd ? (
              <Button onClick={() => setAdding(true)}>新建槽位</Button>
            ) : (
              <div className="flex flex-wrap justify-center gap-2">
                <Button onClick={goNetwork}>前往“节点与网关”</Button>
                <Button variant="outline" onClick={goSubs}>前往“订阅管理”</Button>
              </div>
            )}
          </div>
        )}

        {slots && slots.length > 0 && (
          <div className="tile-grid">
            {slots.map((s) => <SlotTile key={s.id} s={s} state={state!} live={traffic?.slots[s.id]} busy={busy(s.id)} a={actions} onOpen={() => setOpen(s.id)} />)}
          </div>
        )}
      </Card>
      {dialogs}
    </Page>
  );
}

function StateBadges({ s }: { s: Slot }) {
  return (
    <>
      {s.state === "hold" && <Badge variant="danger" title={s.hold_reason}>已断网</Badge>}
      {s.watch?.status === "changed" && <Badge variant="danger" title={s.watch.detail}>出口已变更</Badge>}
      {s.watch?.status === "unreachable" && <Badge variant="warning" title={s.watch.detail}>无法连接</Badge>}
      {s.tmux_bridge && (
        <Badge variant="warning" title={`宿主桥接已开启：槽内经它执行的命令在宿主上运行，使用宿主的权限与网络，不受该槽位的网络限制，断网对其无效。${s.tmux_alive ? "宿主 tmux 正在运行。" : "宿主 tmux 未运行。"}`}>
          宿主桥接
        </Badge>
      )}
      {s.state === "pending" && <Badge variant="warning" title="配置在上次接线后有改动，重新启动后生效">待应用</Badge>}
      {s.state === "offline" && <Badge variant="warning" title="容器正在运行但未接线，槽内无网络">未接线</Badge>}
    </>
  );
}

function SlotTile({ s, state, live, busy, a, onOpen }: { s: Slot; state: State; live?: SlotLive; busy?: string; a: Actions; onOpen: () => void }) {
  const landing = state.nodes.find((n) => n.id === s.landing);
  const wired = isWired(s);
  return (
    <div
      className="tile"
      role="button"
      tabIndex={0}
      aria-label={`槽位 ${s.id}，打开详情`}
      onClick={(e) => !(e.target as HTMLElement).closest("button, [data-menu]") && onOpen()}
      onKeyDown={(e) => e.target === e.currentTarget && (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onOpen())}
    >
      <div className="tile-head">
        <RegionIcon code={landing?.region} from={landing?.region_from} icon={Box} />
        <div className="tile-title">
          <p title={s.note}>{s.id}{s.note && <span className="ml-2 font-sans text-[12px] font-normal text-muted">{s.note}</span>}</p>
          <p><span className={`status-dot mr-1.5 inline-block align-middle ${dotTone(s)}`} />{stateText[s.state]}</p>
        </div>
        <Menu items={menuItems(s, a)} disabled={!!busy} />
      </div>

      <div className="tile-figures">
        <div className="tile-figure"><span>下行</span><strong className="num">{live ? rateText(live.down_bps) : "—"}</strong></div>
        <div className="tile-figure"><span>上行</span><strong className="num">{live ? rateText(live.up_bps) : "—"}</strong></div>
        <div className="tile-figure"><span>连接</span><strong className="num">{live ? live.conns : "—"}</strong></div>
      </div>

      <div className="platform-tile-facts">
        <span className="platform-fact"><span>落地</span><strong>{s.landing}</strong></span>
        {landing?.direct ? <span className="platform-fact"><strong>直出</strong></span> : <span className="platform-fact"><span>网关</span><strong>{s.gateway}</strong></span>}
        <StateBadges s={s} />
      </div>

      <div className="platform-tile-foot">
        <span className="min-w-0 flex-1 truncate" title={wired && s.applied_at ? `最近一次槽内复测通过：${relativeTime(s.applied_at)}` : undefined}>
          {s.exit_ip ? <><span className="mono font-semibold text-fg">{s.exit_ip}</span>{s.exit_where ? ` · ${s.exit_where}` : ""}</> : "尚未探测到出口"}
        </span>
        <MainAction s={s} a={a} busy={busy} />
      </div>
    </div>
  );
}

type DetailTab = "overview" | "traffic" | "env" | "ops";

interface DetailProps {
  s: Slot;
  state: State;
  /** 此刻的流量；没接线或还没采样到时没有。 */
  live?: SlotLive;
  /** 最近 24 小时的趋势。 */
  history: History | null;
  busy?: string;
  a: Actions;
  alerts: React.ReactNode;
  onBack: () => void;
}

/** 链路上的一站。 */
function Hop({ label, name, sub, icon }: { label: string; name: string; sub?: string; icon?: React.ReactNode }) {
  return (
    <div className="chain-hop">
      <span>{label}</span>
      <strong title={name}>{icon}<span className="mono min-w-0 truncate">{name}</span></strong>
      {sub && <small>{sub}</small>}
    </div>
  );
}

function Fact({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <span className="platform-fact">
      <span>{k}</span>
      <strong>{children}</strong>
    </span>
  );
}

function SlotDetail({ s, state, live, history, busy, a, alerts, onBack }: DetailProps) {
  const [tab, setTab] = useState<DetailTab>("overview");
  const wired = isWired(s);
  const landing = state.nodes.find((n) => n.id === s.landing);
  const gateway = state.gateways.find((g) => g.id === s.gateway);
  const gatewayUp = gateway?.container === "running" && gateway.core_ready;
  const limits = [s.cpus ? `${s.cpus} 核` : "", formatBytes(s.memory_bytes)].filter(Boolean).join(" · ");
  const fakes = [s.fake_dmi ? "机型信息" : "", s.fake_proc ? "CPU 与内存" : ""].filter(Boolean).join("、");
  const inheritsTZ = !!s.tz && s.tz === landing?.tz;
  const arrow = <ArrowRight size={16} className="chain-arrow" aria-hidden />;
  const gatewayText = !gateway ? "已不存在" : gatewayUp ? "运行中" : gateway.container === "running" ? "核心未就绪" : "未运行";

  return (
    <Page className="platform-page platform-detail-page">
      <PageHeader
        back={{ label: "全部槽位", onClick: onBack }}
        title={s.id}
        note={s.note}
        subtitle="出口、流量、环境与运维。"
        actions={
          <>
            {wired && !busy && <Button variant="outline" onClick={() => a.enter(s)}><Terminal />进入</Button>}
            <MainAction s={s} a={a} busy={busy} size="default" />
          </>
        }
      />
      {alerts}

      <Card className="platform-directory-card platform-detail-header-card">
        <div className="platform-detail-header-main">
          <div className="flex min-w-0 items-center gap-3">
            <RegionIcon code={landing?.region} from={landing?.region_from} icon={Box} size="detail" />
            <div className="min-w-0">
              <h3 className="truncate">{s.exit_ip && wired ? <span className="mono">{s.exit_ip}</span> : s.id}</h3>
              <p>
                <span className={`status-dot mr-1.5 inline-block align-middle ${dotTone(s)}`} />
                {stateText[s.state]}
                {wired && s.exit_where && ` · ${s.exit_where}`}
                {wired && s.applied_at && ` · ${relativeTime(s.applied_at)}槽内复测通过`}
                {s.state === "hold" && s.hold_reason && ` · ${s.hold_reason}`}
              </p>
            </div>
          </div>
          <div className="platform-detail-header-meta">
            <div className="flex flex-wrap justify-end gap-1.5">
              {wired && !s.watch?.status && <Badge variant="success">已接线</Badge>}
              {s.watch?.status === "ok" && <Badge variant="success">检测正常</Badge>}
              <StateBadges s={s} />
            </div>
            <span>{live ? `↓ ${rateText(live.down_bps)} · ↑ ${rateText(live.up_bps)} · ${live.conns} 条连接` : "无实时流量"}</span>
          </div>
        </div>
        <div className="platform-detail-header-footer">
          <div className="platform-tile-facts">
            <Fact k="落地">{s.landing}</Fact>
            {landing?.direct ? <Fact k="出口方式">直出</Fact> : <Fact k="网关">{s.gateway}</Fact>}
            {gateway?.transit && <Fact k="中转">{gateway.transit}</Fact>}
            <Fact k="用户">{s.user}</Fact>
            <Fact k="时区">{s.tz || "UTC"}</Fact>
            {limits && <Fact k="资源上限">{limits}</Fact>}
          </div>
        </div>
      </Card>

      <Card className="platform-cards-container platform-detail-main-card">
        <Tabs
          label="槽位详情板块"
          value={tab}
          onChange={setTab}
          items={[["overview", "概况"], ["traffic", "流量与连接"], ["env", "环境与挂载"], ["ops", "运维"]]}
        />

        {tab === "overview" && (
          <div className="grid items-stretch gap-3.5 min-[1100px]:grid-cols-2">
            <ExitCard
              report={landing?.exit_report}
              currentIP={wired ? s.exit_ip : undefined}
              busy={busy === "检测中…"}
              onRefresh={() => a.report(s)}
              disabledWhy={gatewayUp ? undefined : "查询需经链路发出：网关未运行，请先启动该槽位"}
            />

            <Card className="panel">
              <div className="panel-head">
                <div>
                  <h3>链路</h3>
                  <p>流量从槽位到出口依次经过的节点</p>
                </div>
              </div>
              <div className="chain">
                <Hop label="槽位" name={s.id} sub={s.user} />
                {arrow}
                <Hop label="网关" name={s.gateway} sub={gatewayText} />
                {gateway?.transit && <>{arrow}<Hop label="中转" name={gateway.transit} /></>}
                {arrow}
                <Hop label="落地" name={s.landing} sub={landing ? `${landing.type} · ${landing.server}:${landing.port}` : "已不存在"} />
                {wired && s.exit_ip && <>{arrow}<Hop label="出口" name={s.exit_ip} sub={s.exit_where} /></>}
              </div>
              <div className="kv-grid">
                <div className="kv">
                  <span>网关</span>
                  <strong className="flex flex-wrap items-center gap-1.5">
                    {gatewayText}
                    {gateway && !gateway.transit && <Tag title="不经中转，直接连接出口节点">不经中转</Tag>}
                    {landing?.direct && <Tag tone="warn" title="直接使用订阅中的节点作为出口：出口地址为多人共用；节点已固定，不会自动更换">直出</Tag>}
                    {gateway && gatewayUp && !gateway.config_current && <Tag tone="warn" title="节点或槽位的设置有改动，重新启动槽位后生效">配置待更新</Tag>}
                  </strong>
                </div>
                {landing?.expect_exit_ip && <div className="kv"><span>预期出口</span><strong className="mono">{landing.expect_exit_ip}</strong></div>}
              </div>
              <div className={`callout ${s.watch?.status === "changed" ? "callout-error" : s.watch?.status === "unreachable" ? "callout-warning" : s.watch?.status === "ok" ? "callout-success" : "callout-info"}`}>
                <ShieldCheck size={15} />
                <span>
                  <b>持续检测：</b>
                  {!s.watch && (wired ? "尚未检测。网页界面运行期间，每隔几分钟检测一次连通性与出口。" : "仅检测已接线的槽位。")}
                  {s.watch?.status === "ok" && `正常，${relativeTime(s.watch.at)}检测，出口未变。`}
                  {s.watch?.status === "unreachable" && `无法连接，自${relativeTime(s.watch.since)}起。槽位当前无网络，出口恢复后自动恢复。`}
                  {s.watch?.status === "changed" && `出口已变更：${s.watch.detail}，已自动断网。`}
                </span>
              </div>
            </Card>
          </div>
        )}

        {tab === "traffic" && <SlotTraffic s={s} live={live} history={history} />}

        {tab === "env" && (
          <div className="platform-detail-tabpanel">
            <div className="platform-drawer-section-head">
              <h4>槽内环境</h4>
              <p>槽内程序可见的身份与硬件信息。</p>
            </div>
            <div className="kv-grid">
              <div className="kv"><span>用户</span><strong className="mono">{s.user}</strong></div>
              <div className="kv"><span>主机名</span><strong className="mono">{s.hostname}</strong></div>
              <div className="kv"><span>HOME</span><strong className="mono">{s.home}</strong></div>
              <div className="kv">
                <span>时区</span>
                <strong className="flex flex-wrap items-center gap-1.5">
                  <span className="mono">{s.tz || "UTC（未设置）"}</span>
                  {inheritsTZ && landing?.tz_fallback && <Tag tone="warn" title="带时区信息的探测地址均无响应，暂用默认值；下次启动时重新探测，也可为落地指定时区">默认值</Tag>}
                  {inheritsTZ && landing?.tz_auto && !landing.tz_fallback && <Tag title="按探测到的出口所在地自动设置，保存在落地上">随出口</Tag>}
                </strong>
              </div>
              <div className="kv"><span>资源上限</span><strong>{limits || "不限制"}</strong></div>
              <div className="kv"><span>硬件信息</span><strong>{fakes ? `按该槽位的设置显示：${fakes}` : "机型信息已屏蔽，CPU 与内存为宿主的实际值"}</strong></div>
            </div>
            {s.tmux_bridge && (
              <div className="callout callout-warning">
                <Terminal size={15} />
                <span>宿主桥接已开启，宿主 tmux {s.tmux_alive ? "正在运行" : "未运行"}。经它执行的命令在宿主上运行，不经该出口。</span>
              </div>
            )}

            <div className="platform-drawer-section-head mt-2">
              <h4>挂载的目录</h4>
              <p>槽内路径与宿主相同，~ 下有同名的快捷方式。</p>
            </div>
            {s.mounts.length === 0 ? (
              <div className="empty-box plain"><p>未挂载项目目录。在项目目录中执行进入命令时，会询问是否挂载。</p></div>
            ) : (
              <div className="flex flex-col gap-2">
                {s.mounts.map((m) => (
                  <div key={m.target} className="flex items-center gap-2">
                    <code className="code-line min-w-0 flex-1">{m.source === m.target ? m.source : `${m.source} → ${m.target}`}</code>
                    {m.read_only && <Tag>只读</Tag>}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {tab === "ops" && (
          <div className="platform-detail-tabpanel platform-ops-tabpanel">
            <div className="platform-drawer-section-head">
              <h4>运维操作</h4>
              <p>以下操作立即对该槽位生效，请谨慎执行。</p>
            </div>
            <div className="platform-ops-list">
              <Op title="进入槽位" hint="网页界面不提供终端，此处给出在本机终端执行的命令。">
                <Button variant="outline" onClick={() => a.enter(s)}><Terminal />进入命令</Button>
              </Op>
              <Op title="重新探测并接线" hint="重新探测链路与出口，通过后为槽位接线；配置有改动时用于应用配置。">
                <Button variant="outline" onClick={() => a.up(s)} disabled={!!busy || s.state === "hold"}><RefreshCw />重新接线</Button>
              </Op>
              <Op title="重建 worker 并接线" hint="删除并重新创建 worker 容器，HOME 与身份保留；槽内正在运行的程序将中断。">
                <Button variant="outline" onClick={() => a.up(s, true)} disabled={!!busy || s.state === "hold"}><RotateCw />重建 worker</Button>
              </Op>
              {s.state === "hold" ? (
                <Op title="解除手动断网" hint="解除后启动槽位即可恢复网络。">
                  <Button variant="outline" onClick={() => a.release(s)} disabled={!!busy}><ShieldCheck />解除断网</Button>
                </Op>
              ) : (
                <Op title="手动断网" hint="立即断开该槽位的网络，容器保留；解除前不会重新接线。">
                  <Button variant="outline" onClick={() => a.hold(s)} disabled={!!busy}><Ban />手动断网</Button>
                </Op>
              )}
              <Op title="备注" hint="仅用于显示，可随时修改，不影响运行中的槽位。">
                <Button variant="outline" onClick={() => a.note(s)}><Pencil />修改备注</Button>
              </Op>
              <Op title="删除槽位" hint="删除 worker 容器并断开接线；默认保留 HOME 与身份。">
                <Button variant="destructive" onClick={() => a.remove(s)} disabled={!!busy}><Trash2 />删除槽位</Button>
              </Op>
            </div>
          </div>
        )}
      </Card>
    </Page>
  );
}

function Op({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <div className="platform-op-item">
      <div className="platform-op-copy">
        <h5>{title}</h5>
        <p className="platform-op-hint">{hint}</p>
      </div>
      {children}
    </div>
  );
}

const emptySlot = (): SlotInput => ({ id: "", note: "", gateway: "", landing: "", mounts: [], memory: "", cpus: 0, user: "user", hostname: "", tz: "", fake_dmi: false, fake_proc: false, tmux_bridge: false });

function AddSlotDialog({ open, state, onClose, onCreated }: { open: boolean; state: State; onClose: () => void; onCreated: (id: string, warnings: string[]) => void }) {
  const landings = state.nodes.filter((n) => n.role === "landing");
  const [slot, setSlot] = useState<SlotInput>(emptySlot);
  const [mounts, setMounts] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [wasOpen, setWasOpen] = useState(false);
  // 出口的两种来源：自己的落地，或者直接用订阅里的一个节点（直出）。
  const [direct, setDirect] = useState(false);
  const [dsub, setDsub] = useState("");
  const [dname, setDname] = useState("");
  const [dnodes, setDnodes] = useState<Delay[] | null>(null);
  const [dfailed, setDfailed] = useState("");

  // 直出时读取所选订阅的可用节点。订阅换得快时，较早发出的请求后到，其结果丢弃。
  useEffect(() => {
    setDname("");
    setDnodes(null);
    setDfailed("");
    if (!open || !direct || !dsub) return;
    let stale = false;
    api.subNodes(dsub).then(
      (all) => {
        if (stale) return;
        const usable = all.filter((d) => !d.info && d.delay_ms >= 0);
        setDnodes(usable);
        setDname(usable[0]?.name ?? "");
      },
      (e) => !stale && setDfailed(errorText(e)),
    );
    return () => {
      stale = true;
    };
  }, [open, direct, dsub]);

  // 每次打开时重置表单，并预选第一个网关和落地。
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) {
      setSlot({ ...emptySlot(), gateway: state.gateways[0]?.id ?? "", landing: landings[0]?.id ?? "" });
      setMounts("");
      setError("");
      setDirect(landings.length === 0 && state.subs.length > 0);
      setDsub(state.subs[0]?.id ?? "");
    }
  }

  const gateway = state.gateways.find((g) => g.id === slot.gateway);
  const sharing = state.slots.filter((s) => s.landing === slot.landing).length;
  const ready = direct ? !!dsub && !!dname : !!slot.gateway && !!slot.landing;

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const base = { ...slot, id: slot.id.trim(), mounts: mounts.split("\n").map((m) => m.trim()).filter(Boolean) };
      const r = await api.addSlot(direct ? { ...base, gateway: "", landing: "", direct_sub: dsub, direct_name: dname } : base);
      onCreated(slot.id.trim(), r.data.warnings);
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
      title="新建槽位"
      busy={saving}
      form
      description="创建后点击“启动”：先探测链路与出口，通过后才为槽位接线。"
      className="!w-[min(840px,calc(100vw-36px))]"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !slot.id.trim() || !ready}>{saving ? "创建中…" : "创建"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <FormSection title="基础">
          <FormGrid>
            <Field label="名称" hint={ID_HINT}>
              <Input aria-label="槽位名称" value={slot.id} maxLength={32} autoFocus placeholder="例如 work-a" onChange={(e) => setSlot({ ...slot, id: e.target.value })} />
            </Field>
            <Field label="备注">
              <Input aria-label="槽位备注" value={slot.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setSlot({ ...slot, note: e.target.value })} />
            </Field>
            <Field label="出口" wide>
              <Select
                aria-label="出口的来源"
                value={direct ? "direct" : "landing"}
                onChange={(e) => setDirect(e.target.value === "direct")}
              >
                <option value="landing">使用自己的落地（固定的出口地址）</option>
                <option value="direct">直出：直接使用订阅中的节点作为出口</option>
              </Select>
            </Field>
            {!direct && (
              <>
                <Field label="网关" hint={gateway?.transit ? "流量经中转到达落地，由落地出站；落地无法连接时槽位无网络，不会改由中转出站。" : undefined}>
                  <Select aria-label="网关" value={slot.gateway} disabled={!state.gateways.length} onChange={(e) => setSlot({ ...slot, gateway: e.target.value })}>
                    {!state.gateways.length && <option value="">暂无网关，请先在“节点与网关”页新建</option>}
                    {state.gateways.map((g) => <option key={g.id} value={g.id}>{g.id}（{g.transit ? `中转 ${g.transit}` : "不经中转"}，已有 {g.slots.length} 个槽位）</option>)}
                  </Select>
                </Field>
                <Field label="落地" warn={sharing > 0 ? `该落地已绑定 ${sharing} 个槽位，它们的出口地址相同；同一落地最多同时为 ${state.landing_limit} 个槽位接线。` : undefined}>
                  <Select aria-label="落地" value={slot.landing} disabled={!landings.length} onChange={(e) => setSlot({ ...slot, landing: e.target.value })}>
                    {!landings.length && <option value="">暂无落地节点，请先在“节点与网关”页添加</option>}
                    {landings.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
                  </Select>
                </Field>
              </>
            )}
            {direct && (
              <>
                <Field label="订阅">
                  <Select aria-label="订阅" value={dsub} disabled={!state.subs.length} onChange={(e) => setDsub(e.target.value)}>
                    {state.subs.length === 0 && <option value="">暂无订阅，请先在“订阅管理”页添加</option>}
                    {state.subs.map((s) => <option key={s.id} value={s.id}>{s.id}（{s.count} 个节点）</option>)}
                  </Select>
                </Field>
                <Field label="节点" warn={dfailed ? `无法读取该订阅的节点：${dfailed}` : dnodes?.length === 0 ? "该订阅中没有可用的节点，请先在“订阅管理”页测试延迟。" : undefined}>
                  <Select aria-label="作为出口的节点" value={dname} disabled={!dnodes?.length} onChange={(e) => setDname(e.target.value)}>
                    {!dnodes && <option value="">{dfailed ? "读取失败" : dsub ? "加载中…" : "请先选择订阅"}</option>}
                    {dnodes?.length === 0 && <option value="">无可用节点</option>}
                    {dnodes?.map((d, i) => <option key={`${i}:${d.name}`} value={d.name}>{d.delay_ms > 0 ? `${d.delay_ms} ms` : "未测试"} · {d.name}</option>)}
                  </Select>
                </Field>
                <div className="callout callout-warning field-span-2">
                  <span>出口即该节点，出口地址为多人共用。节点固定后，其不可用时槽位无网络，不会自动更换节点（否则出口可能切换到其他国家），需手动更换。列表按最近一次测试的延迟排序，不含不可用的节点；尚未测试时，请先在“订阅管理”页测试延迟。</span>
                </div>
              </>
            )}
          </FormGrid>
        </FormSection>

        <FormSection title="项目目录" hint="每行一个本机的绝对路径，槽内路径相同，~ 下有同名的快捷方式。不能挂载整个主目录。">
          <Textarea aria-label="挂载的目录" rows={3} spellCheck={false} placeholder="/data/projects" value={mounts} onChange={(e) => setMounts(e.target.value)} />
        </FormSection>

        <FormSection title="资源与身份" hint="均为选填。">
          <FormGrid>
            <Field label="CPU 核数">
              <Input aria-label="CPU 核数" type="number" min={0} step={0.5} placeholder="不限" value={slot.cpus || ""} onChange={(e) => setSlot({ ...slot, cpus: Number(e.target.value) || 0 })} />
            </Field>
            <Field label="内存上限">
              <Input aria-label="内存上限" placeholder="例如 8g，留空不限" value={slot.memory} onChange={(e) => setSlot({ ...slot, memory: e.target.value })} />
            </Field>
            <Field label="用户名">
              <Input aria-label="用户名" placeholder="user" value={slot.user} onChange={(e) => setSlot({ ...slot, user: e.target.value })} />
            </Field>
            <Field label="主机名">
              <Input aria-label="主机名" placeholder="留空则按“用户名-机型”生成" value={slot.hostname} onChange={(e) => setSlot({ ...slot, hostname: e.target.value })} />
            </Field>
            <Field label="时区" wide hint="留空则沿用落地的时区（首次启动时按出口所在地设置）。">
              <Input aria-label="时区" placeholder="例如 America/Los_Angeles" value={slot.tz} onChange={(e) => setSlot({ ...slot, tz: e.target.value })} />
            </Field>
            <SwitchItem label="覆盖机型信息" hint={slot.fake_dmi ? "使用该槽位自己的机型信息覆盖 DMI" : "默认屏蔽 DMI 信息"} checked={slot.fake_dmi} onChange={(fake_dmi) => setSlot({ ...slot, fake_dmi })} />
            <SwitchItem label="按限额显示 CPU 与内存" hint={slot.fake_proc ? "/proc/cpuinfo 与 meminfo 按上述限额显示（需填写 CPU 或内存）" : "默认显示宿主的 CPU 与内存"} checked={slot.fake_proc} onChange={(fake_proc) => setSlot({ ...slot, fake_proc })} />
            <SwitchItem wide label="宿主 tmux 桥接" hint={slot.tmux_bridge ? "槽内的 Claude 可操作宿主上该槽位专用的 tmux；其中的命令使用宿主的权限与网络，不受该槽位的网络限制" : "默认关闭：槽内无法访问宿主"} checked={slot.tmux_bridge} onChange={(tmux_bridge) => setSlot({ ...slot, tmux_bridge })} />
          </FormGrid>
        </FormSection>

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
