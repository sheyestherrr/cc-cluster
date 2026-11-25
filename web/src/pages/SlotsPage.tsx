// 槽位：一格一张卡片，点开是带页签的详情页（概况、流量与连接、环境与挂载、运维）。
// “卡片网格 + 详情页”的版面和样式取自 Resin（MIT）webui 的 features/platforms/PlatformPage.tsx 与 PlatformDetailPage.tsx，内容是本项目的。
import { ArrowRight, Ban, Box, Copy, Loader2, MoreHorizontal, Pencil, Play, Plus, Power, RefreshCw, RotateCw, ShieldCheck, Sparkles, Terminal, Trash2 } from "lucide-react";
import type React from "react";
import { useState } from "react";
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
import { ApiError, ID_HINT, NOTE_MAX, api, errorText, type Delay, type History, type Slot, type SlotInput, type SlotLive, type SlotState, type State } from "@/lib/api";
import { usePolling } from "@/lib/live";
import { formatBytes, rateText, relativeTime } from "@/lib/utils";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
  goNetwork: () => void;
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

type Failure = { text: string; log: string[] } | null;

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
    { label: "进入的命令…", icon: Terminal, onSelect: () => a.enter(s) },
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

export function SlotsPage({ state, loadError, refresh, goNetwork, open, setOpen }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [adding, setAdding] = useState(false);
  const [entering, setEntering] = useState<Slot | null>(null);
  const [deleting, setDeleting] = useState<Slot | null>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  const [purge, setPurge] = useState(false);
  // 各槽位此刻的流量和最近 24 小时的趋势。
  const traffic = usePolling(api.traffic, 5000);
  const history = usePolling(() => api.history(24), 60_000);

  const run = async (s: Slot, label: string, action: () => Promise<unknown>, done?: string | ((r: unknown) => string)) => {
    setBusy((b) => ({ ...b, [s.id]: label }));
    setFailure(null);
    try {
      const r = await action();
      if (done) toast(typeof done === "string" ? done : done(r));
    } catch (e) {
      setFailure({ text: errorText(e), log: e instanceof ApiError ? e.log : [] });
    } finally {
      setBusy((b) => {
        const { [s.id]: _, ...rest } = b;
        return rest;
      });
      refresh();
    }
  };

  const actions: Actions = {
    up: (s, recreate = false) => run(s, recreate ? "重建中" : "启动中", () => api.slotUp(s.id, recreate), (r) => `${s.id} 已接线，出口 ${(r as { data: { exit: string } }).data.exit}`),
    stop: (s) => run(s, "停止中", () => api.slotStop(s.id), `已停止 ${s.id}`),
    hold: (s) => run(s, "断网中", () => api.slotHold(s.id, "在网页上手动断网"), `已断开 ${s.id} 的网络`),
    release: (s) => run(s, "解除中", () => api.slotRelease(s.id), `已解除 ${s.id} 的手动断网，启动后恢复网络`),
    enter: setEntering,
    note: (s) => setNoting({ kind: "slots", what: "槽位", id: s.id, note: s.note }),
    remove: (s) => {
      setPurge(false);
      setDeleting(s);
    },
    report: (s) => run(s, "检测中", () => api.nodeReport(s.landing), `已更新 ${s.landing} 的出口评估`),
  };

  const slots = state?.slots ?? null;
  // 有网关和落地，或者有订阅（可以直出）就能新建。
  const canAdd = !!state && ((state.gateways.length > 0 && state.nodes.some((n) => n.role === "landing")) || state.subs.length > 0);
  const current = open ? slots?.find((s) => s.id === open) : undefined;

  const alerts = (
    <>
      {loadError && <ErrorBox text={loadError} />}
      {state?.docker_error && <ErrorBox text={`连不上 Docker，下面只显示配置，状态未知：${state.docker_error}`} />}
      {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}
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
            toast(warnings.length ? `已登记槽位 ${id}。\n${warnings.join("\n")}` : `已登记槽位 ${id}，点“启动”接线。`);
            await refresh();
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
        description="网页不提供终端。在这台机器的终端里运行下面的命令；在项目目录里运行时，目录不在挂载范围内会询问是否挂载。"
        className="mid"
        footer={
          <>
            <Button variant="outline" onClick={() => setEntering(null)}>关闭</Button>
            <Button onClick={() => entering && navigator.clipboard.writeText(entering.enter_command).then(() => toast("命令已复制。"), () => toast("复制失败，请手动选中复制。", "error"))}>
              <Copy />
              复制命令
            </Button>
          </>
        }
      >
        <code className="code-line">{entering?.enter_command}</code>
        <p className="field-hint mt-2">把 enter 换成 claude 可以直接启动 Claude Code（首次会提示在槽内安装）。</p>
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
        description="会删除它的 worker 容器并断开接线。默认保留它的 HOME 与身份，以后用同一个名字重新登记时沿用。"
        footer={
          <>
            <Button variant="outline" onClick={() => setDeleting(null)}>取消</Button>
            <Button
              variant="destructive"
              onClick={() => {
                const s = deleting!;
                setDeleting(null);
                if (open === s.id) setOpen(null);
                run(s, "删除中", () => api.removeSlot(s.id, purge), `已删除 ${s.id}`);
              }}
            >
              删除
            </Button>
          </>
        }
      >
        <SwitchItem label="连同 HOME 与身份一起删除" hint="槽内的登录状态和文件都会丢失，无法恢复。" checked={purge} onChange={setPurge} />
      </Dialog>
    </>
  );

  if (current && state) {
    return (
      <>
        <SlotDetail s={current} state={state} live={traffic?.slots[current.id]} history={history} busy={busy[current.id]} a={actions} alerts={alerts} onBack={() => setOpen(null)} />
        {dialogs}
      </>
    );
  }

  return (
    <Page className="platform-page">
      <PageHeader
        title="槽位"
        subtitle="每个槽位是一个没有网络的工作容器，全部流量由网关透明转发到它绑定的落地。"
        actions={
          <>
            <Button variant="outline" onClick={() => refresh()}>
              <RefreshCw />
              刷新
            </Button>
            <Button onClick={() => setAdding(true)} disabled={!canAdd} title={canAdd ? undefined : "先在“节点与网关”里添加一个订阅，或者一个落地节点和一个网关"}>
              <Plus />
              新建槽位
            </Button>
          </>
        }
      />
      {alerts}

      <Card className="platform-cards-container">
        {slots === null && !loadError && <p className="muted">正在读取槽位…</p>}

        {slots && slots.length === 0 && (
          <div className="empty-box plain flex-col !gap-3 !py-12">
            <Sparkles size={20} />
            <p className="text-[15px] font-semibold text-fg">还没有槽位</p>
            <p className="max-w-[460px] text-center text-[13px]">{canAdd ? "新建一个：选好出口（自己的落地，或订阅里的一个节点），再指定要挂进去的项目目录。" : "先去“节点与网关”添加一个订阅，或者一个落地节点和一个网关。"}</p>
            <Button onClick={canAdd ? () => setAdding(true) : goNetwork}>{canAdd ? "新建槽位" : "去节点与网关"}</Button>
          </div>
        )}

        {slots && slots.length > 0 && (
          <div className="tile-grid">
            {slots.map((s) => <SlotTile key={s.id} s={s} state={state!} live={traffic?.slots[s.id]} busy={busy[s.id]} a={actions} onOpen={() => setOpen(s.id)} />)}
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
      {s.watch?.status === "changed" && <Badge variant="danger" title={s.watch.detail}>出口变了</Badge>}
      {s.watch?.status === "unreachable" && <Badge variant="warning" title={s.watch.detail}>连不上</Badge>}
      {s.tmux_bridge && (
        <Badge variant="warning" title={`宿主桥接已开启：槽内经它执行的命令在宿主上运行，使用宿主的权限和宿主网络，不受该槽的网络约束，断网也拦不住。${s.tmux_alive ? "宿主 tmux 正在运行。" : "宿主 tmux 没有在运行。"}`}>
          宿主桥接
        </Badge>
      )}
      {s.state === "pending" && <Badge variant="warning" title="期望配置在上次接线之后变过，重新启动后生效">待应用</Badge>}
      {s.state === "offline" && <Badge variant="warning" title="容器在运行但没有接线，槽内没有网络">未接线</Badge>}
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
      onClick={(e) => !(e.target as HTMLElement).closest("button, [role=menu]") && onOpen()}
      onKeyDown={(e) => e.target === e.currentTarget && (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onOpen())}
    >
      <div className="tile-head">
        <RegionIcon code={landing?.region} from={landing?.region_from} icon={Box} />
        <div className="tile-title">
          <p title={s.note}>{s.id}{s.note && <span className="ml-2 font-sans text-[12px] font-normal text-muted">{s.note}</span>}</p>
          <p><span className={`status-dot mr-1.5 inline-block align-middle ${dotTone(s)}`} />{stateText[s.state]}</p>
        </div>
        <Menu
          trigger={(toggle) => (
            <Button variant="ghost" size="icon" aria-label="更多" onClick={toggle} disabled={!!busy}>
              <MoreHorizontal />
            </Button>
          )}
          items={menuItems(s, a)}
        />
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
        <span className="min-w-0 flex-1 truncate" title={wired && s.applied_at ? `最近一次从槽内复测通过：${relativeTime(s.applied_at)}` : undefined}>
          {s.exit_ip ? <><span className="mono font-semibold text-fg">{s.exit_ip}</span>{s.exit_where ? ` · ${s.exit_where}` : ""}</> : "还没有探测到出口"}
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
  const gatewayText = !gateway ? "已不存在" : gatewayUp ? "运行中" : gateway.container === "running" ? "核心未就绪" : "没有在运行";

  return (
    <Page className="platform-page platform-detail-page">
      <PageHeader
        back={{ label: "全部槽位", onClick: onBack }}
        title={s.id}
        subtitle={s.note || "这个槽位的出口、流量、槽内环境和维护操作。"}
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
                {wired && s.applied_at && ` · ${relativeTime(s.applied_at)}从槽内复测通过`}
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
            <span>{live ? `↓ ${rateText(live.down_bps)} · ↑ ${rateText(live.up_bps)} · ${live.conns} 条连接` : "没有实时流量"}</span>
          </div>
        </div>
        <div className="platform-detail-header-footer">
          <div className="platform-tile-facts">
            <Fact k="落地">{s.landing}</Fact>
            {landing?.direct ? <Fact k="出口方式">直出</Fact> : <Fact k="网关">{s.gateway}</Fact>}
            {gateway?.transit && <Fact k="中转">{gateway.transit}</Fact>}
            <Fact k="用户">{s.user}@{s.hostname}</Fact>
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
              busy={busy === "检测中"}
              onRefresh={() => a.report(s)}
              disabledWhy={gatewayUp ? undefined : "查询要经链路发出：网关没有在运行，先启动这个槽位"}
            />

            <Card className="panel">
              <div className="panel-head">
                <div>
                  <h3>链路</h3>
                  <p>流量从槽位出发，依次经过的每一站</p>
                </div>
              </div>
              <div className="chain">
                <Hop label="槽位" name={s.id} sub={`${s.user}@${s.hostname}`} />
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
                    {gateway && !gateway.transit && <Tag title="不经中转，直接连出口节点">不经中转</Tag>}
                    {landing?.direct && <Tag tone="warn" title="直接用订阅里的节点当出口：多人共用的地址；节点已固定，不会自动更换">直出</Tag>}
                    {gateway && gatewayUp && !gateway.config_current && <Tag tone="warn" title="节点或槽位的设置变过，重新启动槽位后生效">配置待更新</Tag>}
                  </strong>
                </div>
                {landing?.expect_exit_ip && <div className="kv"><span>预期出口</span><strong className="mono">{landing.expect_exit_ip}</strong></div>}
              </div>
              <div className={`callout ${s.watch?.status === "changed" ? "callout-error" : s.watch?.status === "unreachable" ? "callout-warning" : s.watch?.status === "ok" ? "callout-success" : "callout-info"}`}>
                <ShieldCheck size={15} />
                <span>
                  <b>持续检测：</b>
                  {!s.watch && (wired ? "还没轮到；网页开着时每隔几分钟检测一次连通性与出口。" : "只检测已就绪的槽位。")}
                  {s.watch?.status === "ok" && `正常，${relativeTime(s.watch.at)}检测，出口没有变。`}
                  {s.watch?.status === "unreachable" && `连不上，自${relativeTime(s.watch.since)}起；槽位此时没有网络，出口恢复后会自己恢复。`}
                  {s.watch?.status === "changed" && `出口变了：${s.watch.detail}，已自动断网。`}
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
              <p>槽里的程序看到的身份与硬件信息。</p>
            </div>
            <div className="kv-grid">
              <div className="kv"><span>用户</span><strong className="mono">{s.user}@{s.hostname}</strong></div>
              <div className="kv"><span>HOME</span><strong className="mono">{s.home}</strong></div>
              <div className="kv">
                <span>时区</span>
                <strong className="flex flex-wrap items-center gap-1.5">
                  <span className="mono">{s.tz || "UTC（没有设置）"}</span>
                  {inheritsTZ && landing?.tz_fallback && <Tag tone="warn" title="带时区的探测地址都没有应答，暂用兜底值；下次启动会再探测，也可以给落地指定时区">兜底值</Tag>}
                  {inheritsTZ && landing?.tz_auto && !landing.tz_fallback && <Tag title="按出口探测到的所在地自动设置，记在落地上">随出口</Tag>}
                </strong>
              </div>
              <div className="kv"><span>资源上限</span><strong>{limits || "不限制"}</strong></div>
              <div className="kv"><span>硬件信息</span><strong>{fakes ? `按该槽的设置显示：${fakes}` : "机型信息被遮蔽，CPU 与内存是宿主的"}</strong></div>
            </div>
            {s.tmux_bridge && (
              <div className="callout callout-warning">
                <Terminal size={15} />
                <span>宿主桥接已开启，宿主 tmux {s.tmux_alive ? "正在运行" : "没有在运行"}。经它执行的命令在宿主上运行，不走这个出口。</span>
              </div>
            )}

            <div className="platform-drawer-section-head mt-2">
              <h4>挂载的目录</h4>
              <p>槽内使用和宿主相同的路径。</p>
            </div>
            {s.mounts.length === 0 ? (
              <div className="empty-box plain"><p>没有挂载项目目录；在项目目录里运行进入命令时会询问是否挂载。</p></div>
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
              <p>以下操作会立刻对这个槽位生效，请谨慎执行。</p>
            </div>
            <div className="platform-ops-list">
              <Op title="进入槽位" hint="网页不提供终端；给出一条在这台机器的终端里运行的命令。">
                <Button variant="outline" onClick={() => a.enter(s)}><Terminal />进入的命令</Button>
              </Op>
              <Op title="重新探测并接线" hint="重新探测链路和出口，通过后给槽位接线；配置改过时用它来应用。">
                <Button variant="outline" onClick={() => a.up(s)} disabled={!!busy || s.state === "hold"}><RefreshCw />重新接线</Button>
              </Op>
              <Op title="重建 worker 并接线" hint="删掉 worker 容器重新创建，HOME 与身份保留；槽里正在运行的程序会中断。">
                <Button variant="outline" onClick={() => a.up(s, true)} disabled={!!busy || s.state === "hold"}><RotateCw />重建 worker</Button>
              </Op>
              {s.state === "hold" ? (
                <Op title="解除手动断网" hint="解除之后启动槽位，恢复网络。">
                  <Button variant="outline" onClick={() => a.release(s)} disabled={!!busy}><ShieldCheck />解除断网</Button>
                </Op>
              ) : (
                <Op title="手动断网" hint="立刻断开这个槽位的网络，容器保留；解除之前不会再接线。">
                  <Button variant="outline" onClick={() => a.hold(s)} disabled={!!busy}><Ban />手动断网</Button>
                </Op>
              )}
              <Op title="备注" hint="只用于显示，随时可以改，不影响运行中的槽位。">
                <Button variant="outline" onClick={() => a.note(s)}><Pencil />改备注</Button>
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

const emptySlot = (): SlotInput => ({ id: "", note: "", gateway: "", landing: "", mounts: [], memory: "", cpus: 0, user: "", hostname: "", tz: "", fake_dmi: false, fake_proc: false, tmux_bridge: false });

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

  const loadDirect = async (sub: string) => {
    setDsub(sub);
    setDname("");
    setDnodes(null);
    if (!sub) return;
    try {
      const all = (await api.subNodes(sub)).filter((d) => !d.info && d.delay_ms >= 0);
      setDnodes(all);
      setDname(all[0]?.name ?? "");
    } catch (e) {
      setError(errorText(e));
    }
  };

  // 每次打开时重置表单，并预选第一个网关和落地。
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) {
      setSlot({ ...emptySlot(), gateway: state.gateways[0]?.id ?? "", landing: landings[0]?.id ?? "" });
      setMounts("");
      setError("");
      const noLanding = landings.length === 0 && state.subs.length > 0;
      setDirect(noLanding);
      void loadDirect(noLanding ? state.subs[0].id : "");
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
      description="登记后点“启动”：会先探测链路和出口，通过后才给槽位接线。"
      className="!w-[min(840px,100%)]"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !slot.id.trim() || !ready}>{saving ? "登记中…" : "确认登记"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <FormSection title="基础">
          <FormGrid>
            <Field label="名称" hint={ID_HINT}>
              <Input aria-label="槽位名称" value={slot.id} maxLength={32} autoFocus placeholder="例如 work-a" onChange={(e) => setSlot({ ...slot, id: e.target.value })} />
            </Field>
            <Field label="备注（可选）">
              <Input aria-label="槽位备注" value={slot.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setSlot({ ...slot, note: e.target.value })} />
            </Field>
            <Field label="出口" wide>
              <Select
                aria-label="出口的来源"
                value={direct ? "direct" : "landing"}
                onChange={(e) => {
                  const d = e.target.value === "direct";
                  setDirect(d);
                  if (d && !dsub && state.subs[0]) void loadDirect(state.subs[0].id);
                }}
              >
                <option value="landing">用自己的落地（固定的出口地址）</option>
                <option value="direct">直出：直接用订阅里的一个节点当出口</option>
              </Select>
            </Field>
            {!direct && (
              <>
                <Field label="网关" hint={gateway?.transit ? "流量经中转到落地，从落地出去；落地连不上时槽位没网，不会改从中转出去。" : undefined}>
                  <Select aria-label="网关" value={slot.gateway} onChange={(e) => setSlot({ ...slot, gateway: e.target.value })}>
                    {state.gateways.map((g) => <option key={g.id} value={g.id}>{g.id}（{g.transit ? `中转 ${g.transit}` : "不经中转"}，已有 {g.slots.length} 个槽位）</option>)}
                  </Select>
                </Field>
                <Field label="落地" warn={sharing > 0 ? `这个落地已经绑定了 ${sharing} 个槽位，它们的出口地址相同；同一个落地最多同时给 ${state.landing_limit} 个槽位接线。` : undefined}>
                  <Select aria-label="落地" value={slot.landing} onChange={(e) => setSlot({ ...slot, landing: e.target.value })}>
                    {landings.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
                  </Select>
                </Field>
              </>
            )}
            {direct && (
              <>
                <Field label="订阅">
                  <Select aria-label="订阅" value={dsub} onChange={(e) => void loadDirect(e.target.value)}>
                    {state.subs.length === 0 && <option value="">还没有订阅，先在“订阅管理”里添加</option>}
                    {state.subs.map((s) => <option key={s.id} value={s.id}>{s.id}（{s.count} 个节点）</option>)}
                  </Select>
                </Field>
                <Field label="节点">
                  <Select aria-label="当出口的节点" value={dname} disabled={!dnodes} onChange={(e) => setDname(e.target.value)}>
                    {!dnodes && <option value="">读取中…</option>}
                    {dnodes?.map((d) => <option key={d.name} value={d.name}>{d.delay_ms > 0 ? `${d.delay_ms} ms` : "未测"} · {d.name}</option>)}
                  </Select>
                </Field>
                <div className="callout callout-warning field-span-2">
                  <span>出口就是这个节点，是多人共用的地址。节点会被固定：它不通时槽位没网，不会自动换成别的节点（那样可能跳到别的国家），要换请手动改。列表按上次测的延迟排序，不通的没有列出；没测过就先到订阅页测一次。</span>
                </div>
              </>
            )}
          </FormGrid>
        </FormSection>

        <FormSection title="项目目录" hint="每行一个这台机器上的绝对路径；槽内使用相同的路径。之后在项目目录里运行 enter 也会按需挂载。">
          <Textarea aria-label="挂载的目录" rows={3} spellCheck={false} placeholder="/data/projects" value={mounts} onChange={(e) => setMounts(e.target.value)} />
        </FormSection>

        <FormSection title="资源与身份（可不填）">
          <FormGrid>
            <Field label="CPU 核数">
              <Input aria-label="CPU 核数" type="number" min={0} step={0.5} placeholder="不限" value={slot.cpus || ""} onChange={(e) => setSlot({ ...slot, cpus: Number(e.target.value) || 0 })} />
            </Field>
            <Field label="内存上限">
              <Input aria-label="内存上限" placeholder="例如 8g，不填不限" value={slot.memory} onChange={(e) => setSlot({ ...slot, memory: e.target.value })} />
            </Field>
            <Field label="用户名">
              <Input aria-label="用户名" placeholder="不填则随机挑一个常见的名字" value={slot.user} onChange={(e) => setSlot({ ...slot, user: e.target.value })} />
            </Field>
            <Field label="主机名">
              <Input aria-label="主机名" placeholder="不填则按“用户名-机型”生成" value={slot.hostname} onChange={(e) => setSlot({ ...slot, hostname: e.target.value })} />
            </Field>
            <Field label="时区" wide hint="不填则沿用落地的时区（首次启动时按出口所在地设置）。">
              <Input aria-label="时区" placeholder="例如 America/Los_Angeles" value={slot.tz} onChange={(e) => setSlot({ ...slot, tz: e.target.value })} />
            </Field>
            <SwitchItem label="覆盖机型信息" hint={slot.fake_dmi ? "用这个槽位自己的机型信息覆盖 DMI" : "默认把 DMI 遮成空的"} checked={slot.fake_dmi} onChange={(fake_dmi) => setSlot({ ...slot, fake_dmi })} />
            <SwitchItem label="按限额显示 CPU 与内存" hint={slot.fake_proc ? "/proc/cpuinfo 与 meminfo 按上面的限额显示（需要填 CPU 或内存）" : "默认显示宿主机的 CPU 与内存"} checked={slot.fake_proc} onChange={(fake_proc) => setSlot({ ...slot, fake_proc })} />
            <SwitchItem wide label="宿主 tmux 桥接" hint={slot.tmux_bridge ? "槽内的 Claude 可以操作宿主上该槽专用的 tmux；其中的命令使用宿主的权限和宿主网络，不受该槽的网络约束" : "默认关闭：槽内碰不到宿主"} checked={slot.tmux_bridge} onChange={(tmux_bridge) => setSlot({ ...slot, tmux_bridge })} />
          </FormGrid>
        </FormSection>

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
