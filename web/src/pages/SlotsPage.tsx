// 改编自 Claude-Hydra（MIT）app/src/pages/AccountsPage.tsx，v1.1.6：
// 沿用列表行、行内菜单与删除确认的结构，内容换成 cc-cluster 的槽位。
import { Ban, ChevronRight, Copy, Loader2, MoreHorizontal, Play, Plus, Power, RefreshCw, RotateCw, ShieldCheck, Terminal, Trash2 } from "lucide-react";
import { useState } from "react";
import { ExitCard } from "@/components/ExitCard";
import { Menu } from "@/components/Menu";
import { DetailEmpty, DetailGrid, DetailHeader, ErrorBox, KeyValue, Line, Page, PageHeader, RowTitle, Tag, selectableRow } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useToast } from "@/components/ui/toast";
import { ApiError, api, errorText, type Delay, type Slot, type SlotInput, type SlotState, type State } from "@/lib/api";
import { PALETTE, badgeLetter, cn, colorIndex, formatBytes, relativeTime } from "@/lib/utils";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
  goNetwork: () => void;
}

const stateText: Record<SlotState, string> = {
  ready: "已接线",
  pending: "已接线 · 配置待应用",
  offline: "运行中 · 未接线",
  stopped: "已停止",
  absent: "未创建",
  hold: "手动断网",
  unknown: "状态未知",
};

type Failure = { text: string; log: string[] } | null;

export function SlotsPage({ state, loadError, refresh, goNetwork }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [adding, setAdding] = useState(false);
  const [entering, setEntering] = useState<Slot | null>(null);
  const [deleting, setDeleting] = useState<Slot | null>(null);
  const [purge, setPurge] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);

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

  const up = (s: Slot, recreate = false) =>
    run(s, recreate ? "重建中" : "启动中", () => api.slotUp(s.id, recreate), (r) => `${s.id} 已接线，出口 ${(r as { data: { exit: string } }).data.exit}`);

  const slots = state?.slots ?? null;
  // 有网关和落地，或者有订阅（可以直出）就能新建。
  const canAdd = !!state && ((state.gateways.length > 0 && state.nodes.some((n) => n.role === "landing")) || state.subs.length > 0);
  // 详情栏显示选中的槽位；没选过或选中的已被删除时显示第一个。
  const current = slots?.find((s) => s.id === selected) ?? slots?.[0] ?? null;
  const stop = (s: Slot) => run(s, "停止中", () => api.slotStop(s.id), `已停止 ${s.id}`);
  const release = (s: Slot) => run(s, "解除中", () => api.slotRelease(s.id), `已解除 ${s.id} 的手动断网，启动后恢复网络`);

  const side = !state ? (
    <DetailEmpty>读取中…</DetailEmpty>
  ) : !current ? (
    <DetailEmpty>新建槽位后，这里显示它的出口评估、链路和槽内环境。</DetailEmpty>
  ) : (
    <SlotDetail
      key={current.id}
      s={current}
      state={state}
      busy={busy[current.id]}
      onUp={() => up(current)}
      onStop={() => stop(current)}
      onRelease={() => release(current)}
      onEnter={() => setEntering(current)}
      onReport={() => run(current, "检测中", () => api.nodeReport(current.landing), `已更新 ${current.landing} 的出口评估`)}
    />
  );

  return (
    <Page side={side}>
      <PageHeader
        title="槽位"
        subtitle="每个槽位是一个没有网络的工作容器，全部流量由网关透明转发到它绑定的落地。"
        actions={
          <>
            <Button variant="ghost" size="iconSm" aria-label="刷新" title="刷新" onClick={() => refresh()}>
              <RefreshCw />
            </Button>
            <Button size="sm" onClick={() => setAdding(true)} disabled={!canAdd} title={canAdd ? undefined : "先在“节点与网关”里添加一个订阅，或者一个落地节点和一个网关"}>
              <Plus />
              新建槽位
            </Button>
          </>
        }
      />

      <div className="space-y-4 px-5">
        {loadError && <ErrorBox text={loadError} />}
        {state?.docker_error && <ErrorBox text={`连不上 Docker，下面只显示配置，状态未知：${state.docker_error}`} />}
        {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}

        {slots && slots.length === 0 && (
          <button
            onClick={canAdd ? () => setAdding(true) : goNetwork}
            className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed border-line px-4 py-10 text-muted hover:border-faint hover:text-fg active:!scale-100"
          >
            <Plus className="h-5 w-5" />
            <span className="text-[13px] font-medium">还没有槽位</span>
            <span className="text-[12px]">{canAdd ? "新建一个：选好出口（自己的落地，或订阅里的一个节点），再指定要挂进去的项目目录" : "先去“节点与网关”添加一个订阅，或者一个落地节点和一个网关"}</span>
          </button>
        )}

        {slots && slots.length > 0 && (
          <div className="list-card">
            {slots.map((s) => (
              <SlotRow
                key={s.id}
                s={s}
                busy={busy[s.id]}
                selected={current?.id === s.id}
                onSelect={() => setSelected(s.id)}
                onUp={() => up(s)}
                onRecreate={() => up(s, true)}
                onStop={() => stop(s)}
                onHold={() => run(s, "断网中", () => api.slotHold(s.id, "在网页上手动断网"), `已断开 ${s.id} 的网络`)}
                onRelease={() => release(s)}
                onEnter={() => setEntering(s)}
                onDelete={() => {
                  setPurge(false);
                  setDeleting(s);
                }}
              />
            ))}
          </div>
        )}

        {slots === null && !loadError && (
          <div className="list-card">
            {[0, 1].map((i) => (
              <div key={i} className="flex items-center gap-3 px-4 py-3.5">
                <div className="h-8 w-8 animate-pulse rounded-[9px] bg-pill" />
                <div className="space-y-1.5"><div className="h-3 w-28 animate-pulse rounded bg-pill" /><div className="h-2.5 w-44 animate-pulse rounded bg-pill" /></div>
              </div>
            ))}
          </div>
        )}
      </div>

      {state && <AddSlotDialog open={adding} state={state} onClose={() => setAdding(false)} onCreated={async (id, warnings) => {
        setAdding(false);
        toast(warnings.length ? `已登记槽位 ${id}。\n${warnings.join("\n")}` : `已登记槽位 ${id}，点“启动”接线。`);
        await refresh();
      }} />}

      <Dialog
        open={!!entering}
        onClose={() => setEntering(null)}
        title={`进入 ${entering?.id ?? ""}`}
        description="网页不提供终端。在这台机器的终端里运行下面的命令；在项目目录里运行时，目录不在挂载范围内会询问是否挂载。"
        className="max-w-[560px]"
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
        <p className="selectable break-all rounded-lg bg-pill px-3 py-2 font-mono text-[11.5px] leading-[18px] text-fg-2">{entering?.enter_command}</p>
        <p className="mt-2 text-[12px] leading-[18px] text-muted">把 enter 换成 claude 可以直接启动 Claude Code（首次会提示在槽内安装）。</p>
        {!!entering?.mounts.length && (
          <div className="mt-3">
            <div className="group-label">已挂载的目录</div>
            <div className="selectable space-y-1 font-mono text-[11.5px] text-fg-2">
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
                run(s, "删除中", () => api.removeSlot(s.id, purge), `已删除 ${s.id}`);
              }}
            >
              删除
            </Button>
          </>
        }
      >
        <label className="flex items-center gap-2.5 text-[12.5px] text-fg-2">
          <Switch checked={purge} onCheckedChange={setPurge} label="连同 HOME 与身份一起删除" />
          连同 HOME 与身份一起删除（槽内的登录状态和文件都会丢失，无法恢复）
        </label>
      </Dialog>
    </Page>
  );
}

interface RowProps {
  s: Slot;
  busy?: string;
  selected: boolean;
  onSelect: () => void;
  onUp: () => void;
  onRecreate: () => void;
  onStop: () => void;
  onHold: () => void;
  onRelease: () => void;
  onEnter: () => void;
  onDelete: () => void;
}

function StateDot({ s }: { s: Slot }) {
  const wired = s.state === "ready" || s.state === "pending";
  return <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", wired ? "bg-ok shadow-[0_0_0_3px_rgb(var(--ok-soft))]" : s.state === "hold" ? "bg-danger" : "bg-faint")} />;
}

function SlotRow({ s, busy, selected, onSelect, onUp, onRecreate, onStop, onHold, onRelease, onEnter, onDelete }: RowProps) {
  const wired = s.state === "ready" || s.state === "pending";
  const running = wired || s.state === "offline";
  return (
    <div {...selectableRow(selected, onSelect)}>
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[9px] text-[13px] font-bold text-white" style={{ backgroundColor: PALETTE[colorIndex(s.id)] }} aria-hidden>
        {badgeLetter(s.id)}
      </span>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <RowTitle selected={selected} onSelect={onSelect}>{s.id}</RowTitle>
          <Tag tone="env" title="落地节点">{s.landing}</Tag>
          {s.state === "hold" && <Tag tone="danger" title={s.hold_reason}>已断网</Tag>}
          {s.tmux_bridge && (
            <Tag tone="warn" title={`宿主桥接已开启：槽内经它执行的命令在宿主上运行，使用宿主的权限和宿主网络，不受该槽的网络约束，断网也拦不住。${s.tmux_alive ? "宿主 tmux 正在运行。" : "宿主 tmux 没有在运行。"}`}>
              宿主桥接
            </Tag>
          )}
          {s.state === "pending" && <Tag tone="warn" title="期望配置在上次接线之后变过，重新启动后生效">待应用</Tag>}
          {s.state === "offline" && <Tag tone="warn" title="容器在运行但没有接线，槽内没有网络">未接线</Tag>}
        </div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11.5px] text-muted">
          <StateDot s={s} />
          <span className="shrink-0">{stateText[s.state]}</span>
          {s.exit_ip && (
            <>
              <span className="shrink-0 text-faint">·</span>
              <span className="selectable min-w-0 truncate" title={`最近一次从槽内复测通过：${relativeTime(s.applied_at)}`}>
                出口 {s.exit_ip}{s.exit_where ? `（${s.exit_where}）` : ""}
              </span>
            </>
          )}
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-1">
        {busy ? (
          <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            {busy}
          </span>
        ) : (
          <Button variant="outline" size="sm" className="min-w-[72px]" onClick={running ? onStop : s.state === "hold" ? onRelease : onUp} disabled={s.state === "unknown"}>
            {running ? <><Power />停止</> : s.state === "hold" ? <><ShieldCheck />解除</> : <><Play className="!h-3 !w-3" />启动</>}
          </Button>
        )}
        <Menu
          trigger={(toggle) => (
            <Button variant="ghost" size="iconSm" aria-label="更多" onClick={toggle} disabled={!!busy}>
              <MoreHorizontal />
            </Button>
          )}
          items={[
            { label: "进入的命令…", icon: Terminal, onSelect: onEnter },
            { label: "重新探测并接线", icon: RefreshCw, onSelect: onUp, disabled: s.state === "hold" },
            { label: "重建 worker 并接线", icon: RotateCw, onSelect: onRecreate, disabled: s.state === "hold" },
            s.state === "hold"
              ? { label: "解除手动断网", icon: ShieldCheck, onSelect: onRelease }
              : { label: "手动断网", icon: Ban, onSelect: onHold },
            "sep",
            { label: "删除…", icon: Trash2, onSelect: onDelete, danger: true },
          ]}
        />
      </div>
    </div>
  );
}

/** 链路上的一站。 */
function Hop({ label, name, tone }: { label: string; name: string; tone?: "brand" | "ok" }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span className="text-[10.5px] leading-4 text-muted">{label}</span>
      <span
        className={cn(
          "selectable max-w-[180px] truncate rounded-md px-1.5 py-0.5 font-mono text-[11.5px] leading-[18px]",
          tone === "brand" ? "bg-brand-soft text-brand" : tone === "ok" ? "bg-ok-soft text-ok" : "bg-pill text-fg-2",
        )}
        title={name}
      >
        {name}
      </span>
    </span>
  );
}

interface DetailProps {
  s: Slot;
  state: State;
  busy?: string;
  onUp: () => void;
  onStop: () => void;
  onRelease: () => void;
  onEnter: () => void;
  onReport: () => void;
}

/** 右边的详情栏：出口评估、链路、槽内环境和挂载。 */
function SlotDetail({ s, state, busy, onUp, onStop, onRelease, onEnter, onReport }: DetailProps) {
  const wired = s.state === "ready" || s.state === "pending";
  const running = wired || s.state === "offline";
  const landing = state.nodes.find((n) => n.id === s.landing);
  const gateway = state.gateways.find((g) => g.id === s.gateway);
  const gatewayUp = gateway?.container === "running" && gateway.core_ready;
  const limits = [s.cpus ? `${s.cpus} 核` : "", formatBytes(s.memory_bytes)].filter(Boolean).join(" · ");
  const fakes = [s.fake_dmi ? "机型信息" : "", s.fake_proc ? "CPU 与内存" : ""].filter(Boolean).join("、");
  const inheritsTZ = !!s.tz && s.tz === landing?.tz;
  const arrow = <ChevronRight className="mt-4 h-3.5 w-3.5 shrink-0 text-faint" aria-hidden />;

  return (
    <>
      <DetailHeader
        badge={
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] text-[14px] font-bold text-white" style={{ backgroundColor: PALETTE[colorIndex(s.id)] }} aria-hidden>
            {badgeLetter(s.id)}
          </span>
        }
        title={s.id}
        status={
          <>
            <StateDot s={s} />
            <span>{stateText[s.state]}</span>
            {wired && s.applied_at && <span className="truncate">· {relativeTime(s.applied_at)}从槽内复测通过</span>}
            {s.state === "hold" && s.hold_reason && <span className="truncate">· {s.hold_reason}</span>}
          </>
        }
        actions={
          busy ? (
            <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted"><Loader2 className="h-3.5 w-3.5 animate-spin" />{busy}</span>
          ) : (
            <>
              {wired && <Button variant="outline" size="sm" onClick={onEnter}><Terminal />进入</Button>}
              <Button variant={running ? "outline" : "default"} size="sm" className="min-w-[72px]" onClick={running ? onStop : s.state === "hold" ? onRelease : onUp} disabled={s.state === "unknown"}>
                {running ? <><Power />停止</> : s.state === "hold" ? <><ShieldCheck />解除断网</> : <><Play className="!h-3 !w-3" />启动</>}
              </Button>
            </>
          )
        }
      />

      <DetailGrid>
        <ExitCard
          report={landing?.exit_report}
          currentIP={wired ? s.exit_ip : undefined}
          busy={busy === "检测中"}
          onRefresh={onReport}
          disabledWhy={gatewayUp ? undefined : "查询要经链路发出：网关没有在运行，先启动这个槽位"}
        />

        <section>
          <div className="group-label">链路</div>
          <div className="list-card">
            <div className="flex flex-wrap items-start gap-x-1.5 gap-y-2 px-4 py-3">
              <Hop label="槽位" name={s.id} />
              {arrow}
              <Hop label="网关" name={s.gateway} />
              {gateway?.transit && <>{arrow}<Hop label="中转" name={gateway.transit} /></>}
              {arrow}
              <Hop label="落地" name={s.landing} tone="brand" />
              {wired && s.exit_ip && <>{arrow}<Hop label="出口" name={s.exit_ip} tone="ok" /></>}
            </div>
            <KeyValue k="网关" plain>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-fg-2">
                {!gateway ? "已不存在" : gatewayUp ? "运行中" : gateway.container === "running" ? "核心未就绪" : "没有在运行"}
                {gateway && !gateway.transit && <Tag title="不经中转，直接连出口节点">不经中转</Tag>}
                {landing?.direct && <Tag tone="warn" title="直接用订阅里的节点当出口：多人共用的地址；节点已固定，不会自动更换">直出</Tag>}
                {gateway && gatewayUp && !gateway.config_current && <Tag tone="warn" title="节点或槽位的设置变过，重新启动槽位后生效">配置待更新</Tag>}
              </span>
            </KeyValue>
            <KeyValue k="落地" v={landing ? `${landing.type} · ${landing.server}:${landing.port}` : "已不存在"} />
            {landing?.expect_exit_ip && <KeyValue k="预期出口" v={landing.expect_exit_ip} />}
          </div>
        </section>

        <section>
          <div className="group-label">槽内环境</div>
          <div className="list-card">
            <KeyValue k="用户" v={`${s.user}@${s.hostname}`} />
            <KeyValue k="HOME" v={s.home} />
            <KeyValue k="时区" plain>
              <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-fg-2">
                <span className="selectable font-mono text-[11.5px]">{s.tz || "UTC（没有设置）"}</span>
                {inheritsTZ && landing?.tz_fallback && <Tag tone="warn" title="带时区的探测地址都没有应答，暂用兜底值；下次启动会再探测，也可以给落地指定时区">兜底值</Tag>}
                {inheritsTZ && landing?.tz_auto && !landing.tz_fallback && <Tag title="按出口探测到的所在地自动设置，记在落地上">随出口</Tag>}
              </span>
            </KeyValue>
            <KeyValue k="资源上限" v={limits || "不限制"} plain />
            <KeyValue k="硬件信息" v={fakes ? `按该槽的设置显示：${fakes}` : "机型信息被遮蔽，CPU 与内存是宿主的"} plain />
            {s.tmux_bridge && (
              <KeyValue k="宿主桥接" plain>
                <span className="min-w-0 leading-[18px] text-warn">
                  已开启，宿主 tmux {s.tmux_alive ? "正在运行" : "没有在运行"}。经它执行的命令在宿主上运行，不走这个出口。
                </span>
              </KeyValue>
            )}
          </div>
        </section>

        <section>
          <div className="group-label">挂载的目录</div>
          <div className="list-card">
            {s.mounts.length === 0 && <div className="px-4 py-3 text-[12.5px] text-muted">没有挂载项目目录；在项目目录里运行进入命令时会询问是否挂载。</div>}
            {s.mounts.map((m) => (
              <div key={m.target} className="selectable flex items-center gap-2 px-4 py-2.5 font-mono text-[11.5px] leading-[18px] text-fg-2">
                <span className="min-w-0 break-all">{m.source === m.target ? m.source : `${m.source} → ${m.target}`}</span>
                {m.read_only && <Tag>只读</Tag>}
              </div>
            ))}
          </div>
        </section>
      </DetailGrid>
    </>
  );
}

const emptySlot = (): SlotInput => ({ id: "", gateway: "", landing: "", mounts: [], memory: "", cpus: 0, user: "", hostname: "", tz: "", fake_dmi: false, fake_proc: false, tmux_bridge: false });

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
      className="max-w-[600px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !slot.id.trim() || !ready}>{saving ? "登记中…" : "登记"}</Button>
        </>
      }
    >
      <div className="space-y-5">
        <section>
          <div className="group-label">基础</div>
          <div className="list-card">
            <Line label="名称">
              <Input aria-label="槽位名称" value={slot.id} maxLength={32} autoFocus placeholder="小写字母、数字、连字符，例如 work-a" onChange={(e) => setSlot({ ...slot, id: e.target.value })} />
            </Line>
            <Line label="出口">
              <Select aria-label="出口的来源" value={direct ? "direct" : "landing"} onChange={(e) => {
                const d = e.target.value === "direct";
                setDirect(d);
                if (d && !dsub && state.subs[0]) void loadDirect(state.subs[0].id);
              }}>
                <option value="landing">用自己的落地（固定的出口地址）</option>
                <option value="direct">直出：直接用订阅里的一个节点当出口</option>
              </Select>
            </Line>
            {!direct && (
              <>
                <Line label="网关">
                  <Select aria-label="网关" value={slot.gateway} onChange={(e) => setSlot({ ...slot, gateway: e.target.value })}>
                    {state.gateways.map((g) => <option key={g.id} value={g.id}>{g.id}（{g.transit ? `中转 ${g.transit}` : "不经中转"}，已有 {g.slots.length} 个槽位）</option>)}
                  </Select>
                </Line>
                <Line label="落地">
                  <Select aria-label="落地" value={slot.landing} onChange={(e) => setSlot({ ...slot, landing: e.target.value })}>
                    {landings.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
                  </Select>
                </Line>
                {gateway?.transit && (
                  <p className="px-4 pb-3 pl-[116px] text-[11.5px] leading-[18px] text-muted">流量经中转到落地，从落地出去；落地连不上时槽位没网，不会改从中转出去。</p>
                )}
                {sharing > 0 && (
                  <p className="px-4 pb-3 pl-[116px] text-[11.5px] leading-[18px] text-warn">这个落地已经绑定了 {sharing} 个槽位，它们的出口地址相同；同一个落地最多同时给 {state.landing_limit} 个槽位接线。</p>
                )}
              </>
            )}
            {direct && (
              <>
                <Line label="订阅">
                  <Select aria-label="订阅" value={dsub} onChange={(e) => void loadDirect(e.target.value)}>
                    {state.subs.length === 0 && <option value="">还没有订阅，先在“节点与网关”里添加</option>}
                    {state.subs.map((s) => <option key={s.id} value={s.id}>{s.id}（{s.count} 个节点）</option>)}
                  </Select>
                </Line>
                <Line label="节点">
                  <Select aria-label="当出口的节点" value={dname} disabled={!dnodes} onChange={(e) => setDname(e.target.value)}>
                    {!dnodes && <option value="">读取中…</option>}
                    {dnodes?.map((d) => <option key={d.name} value={d.name}>{d.delay_ms > 0 ? `${d.delay_ms} ms` : "未测"} · {d.name}</option>)}
                  </Select>
                </Line>
                <p className="px-4 pb-3 pl-[116px] text-[11.5px] leading-[18px] text-warn">
                  出口就是这个节点，是多人共用的地址。节点会被固定：它不通时槽位没网，不会自动换成别的节点（那样可能跳到别的国家），要换请手动改。列表按上次测的延迟排序，不通的没有列出；没测过就先到订阅的“选节点”里测一次。
                </p>
              </>
            )}
          </div>
        </section>

        <section>
          <div className="group-label">项目目录</div>
          <div className="list-card">
            <div className="px-4 py-3">
              <Textarea aria-label="挂载的目录" rows={3} spellCheck={false} placeholder={"每行一个这台机器上的绝对路径，例如 /data/projects\n槽内使用相同的路径；之后在项目目录里运行 enter 也会按需挂载"} value={mounts} onChange={(e) => setMounts(e.target.value)} />
            </div>
          </div>
        </section>

        <section>
          <div className="group-label">资源与身份（可不填）</div>
          <div className="list-card">
            <Line label="CPU 与内存">
              <Input aria-label="CPU 核数" type="number" min={0} step={0.5} placeholder="核数，不限" value={slot.cpus || ""} onChange={(e) => setSlot({ ...slot, cpus: Number(e.target.value) || 0 })} />
              <Input aria-label="内存上限" placeholder="内存，例如 8g" value={slot.memory} onChange={(e) => setSlot({ ...slot, memory: e.target.value })} />
            </Line>
            <Line label="用户名">
              <Input aria-label="用户名" placeholder="不填则随机挑一个常见的名字" value={slot.user} onChange={(e) => setSlot({ ...slot, user: e.target.value })} />
            </Line>
            <Line label="主机名">
              <Input aria-label="主机名" placeholder="不填则按“用户名-机型”生成" value={slot.hostname} onChange={(e) => setSlot({ ...slot, hostname: e.target.value })} />
            </Line>
            <Line label="时区">
              <Input aria-label="时区" placeholder="不填则沿用落地的时区（首次启动时按出口所在地设置）" value={slot.tz} onChange={(e) => setSlot({ ...slot, tz: e.target.value })} />
            </Line>
            <Line label="机型信息">
              <Switch checked={slot.fake_dmi} onCheckedChange={(fake_dmi) => setSlot({ ...slot, fake_dmi })} label="覆盖 DMI 机型信息" />
              <span className="text-[11.5px] text-muted">{slot.fake_dmi ? "用这个槽位自己的机型信息覆盖 DMI" : "默认把 DMI 遮成空的"}</span>
            </Line>
            <Line label="CPU / 内存显示">
              <Switch checked={slot.fake_proc} onCheckedChange={(fake_proc) => setSlot({ ...slot, fake_proc })} label="按限额显示 CPU 与内存" />
              <span className="text-[11.5px] text-muted">{slot.fake_proc ? "/proc/cpuinfo 与 meminfo 按上面的限额显示（需要填 CPU 或内存）" : "显示宿主机的 CPU 与内存"}</span>
            </Line>
            <Line label="宿主桥接">
              <Switch checked={slot.tmux_bridge} onCheckedChange={(tmux_bridge) => setSlot({ ...slot, tmux_bridge })} label="开启宿主 tmux 桥接" />
              <span className="text-[11.5px] text-muted">{slot.tmux_bridge ? "槽内的 Claude 可以操作宿主上该槽专用的 tmux；其中的命令使用宿主的权限和宿主网络，不受该槽的网络约束" : "默认关闭：槽内碰不到宿主"}</span>
            </Line>
          </div>
        </section>

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
