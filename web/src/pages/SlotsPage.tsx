// 改编自 Claude-Hydra（MIT）app/src/pages/AccountsPage.tsx，v1.1.6：
// 沿用列表行、行内菜单与删除确认的结构，内容换成 cc-cluster 的槽位。
import { Ban, Copy, Loader2, MoreHorizontal, Play, Plus, Power, RefreshCw, RotateCw, ShieldCheck, Terminal, Trash2 } from "lucide-react";
import { useState } from "react";
import { Menu } from "@/components/Menu";
import { ErrorBox, Line, Page, PageHeader, Tag } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useToast } from "@/components/ui/toast";
import { ApiError, api, errorText, type Slot, type SlotInput, type SlotState, type State } from "@/lib/api";
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
  const canAdd = !!state && state.gateways.length > 0 && state.nodes.some((n) => n.role === "landing");

  return (
    <Page>
      <PageHeader
        title="槽位"
        subtitle="每个槽位是一个没有网络的工作容器，全部流量由网关透明转发到它绑定的落地。"
        actions={
          <>
            <Button variant="ghost" size="iconSm" aria-label="刷新" title="刷新" onClick={() => refresh()}>
              <RefreshCw />
            </Button>
            <Button size="sm" onClick={() => setAdding(true)} disabled={!canAdd} title={canAdd ? undefined : "先在“节点与网关”里添加落地节点和网关"}>
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
            <span className="text-[12px]">{canAdd ? "新建一个：选好网关和落地，再指定要挂进去的项目目录" : "先去“节点与网关”添加一个落地节点和一个网关"}</span>
          </button>
        )}

        {slots && slots.length > 0 && (
          <div className="list-card">
            {slots.map((s) => (
              <SlotRow
                key={s.id}
                s={s}
                busy={busy[s.id]}
                onUp={() => up(s)}
                onRecreate={() => up(s, true)}
                onStop={() => run(s, "停止中", () => api.slotStop(s.id), `已停止 ${s.id}`)}
                onHold={() => run(s, "断网中", () => api.slotHold(s.id, "在网页上手动断网"), `已断开 ${s.id} 的网络`)}
                onRelease={() => run(s, "解除中", () => api.slotRelease(s.id), `已解除 ${s.id} 的手动断网，启动后恢复网络`)}
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
  onUp: () => void;
  onRecreate: () => void;
  onStop: () => void;
  onHold: () => void;
  onRelease: () => void;
  onEnter: () => void;
  onDelete: () => void;
}

function SlotRow({ s, busy, onUp, onRecreate, onStop, onHold, onRelease, onEnter, onDelete }: RowProps) {
  const wired = s.state === "ready" || s.state === "pending";
  const running = wired || s.state === "offline";
  const limits = [s.cpus ? `${s.cpus} 核` : "", formatBytes(s.memory_bytes)].filter(Boolean).join(" · ");
  return (
    <div className="group flex items-center gap-3 px-4 py-3 hover:bg-card-2">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[9px] text-[13px] font-bold text-white" style={{ backgroundColor: PALETTE[colorIndex(s.id)] }} aria-hidden>
        {badgeLetter(s.id)}
      </span>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate text-[13.5px] font-medium">{s.id}</span>
          <Tag tone="env" title="落地节点">{s.landing}</Tag>
          <Tag title="网关">{s.gateway}</Tag>
          {s.tz && <Tag title="槽内时区">{s.tz}</Tag>}
          {s.state === "hold" && <Tag tone="danger" title={s.hold_reason}>已断网</Tag>}
          {s.state === "pending" && <Tag tone="warn" title="期望配置在上次接线之后变过，重新启动后生效">待应用</Tag>}
          {s.state === "offline" && <Tag tone="warn" title="容器在运行但没有接线，槽内没有网络">未接线</Tag>}
        </div>
        <div className="mt-0.5 flex items-center gap-1.5 text-[11.5px] text-muted">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", wired ? "bg-ok shadow-[0_0_0_3px_rgb(var(--ok-soft))]" : s.state === "hold" ? "bg-danger" : "bg-faint")} />
          <span className="shrink-0">{stateText[s.state]}</span>
          {s.exit_ip && (
            <>
              <span className="shrink-0 text-faint">·</span>
              <span className="selectable min-w-0 truncate" title={`最近一次从槽内复测通过：${relativeTime(s.applied_at)}`}>
                出口 {s.exit_ip}{s.exit_where ? `（${s.exit_where}）` : ""}
              </span>
            </>
          )}
          <span className="shrink-0 text-faint">·</span>
          <span className="min-w-0 truncate" title={`用户 ${s.user}，主机名 ${s.hostname}${limits ? `，${limits}` : ""}`}>{s.user}@{s.hostname}</span>
        </div>
      </div>

      <div className="flex shrink-0 items-center gap-1">
        {busy ? (
          <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            {busy}
          </span>
        ) : (
          <>
            {wired && <Button variant="ghost" size="sm" onClick={onEnter}><Terminal />进入</Button>}
            <Button variant="outline" size="sm" className="min-w-[72px]" onClick={running ? onStop : s.state === "hold" ? onRelease : onUp} disabled={s.state === "unknown"}>
              {running ? <><Power />停止</> : s.state === "hold" ? <><ShieldCheck />解除</> : <><Play className="!h-3 !w-3" />启动</>}
            </Button>
          </>
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

const emptySlot = (): SlotInput => ({ id: "", gateway: "", landing: "", mounts: [], memory: "", cpus: 0, user: "", hostname: "", tz: "", fake_dmi: false, fake_proc: false });

function AddSlotDialog({ open, state, onClose, onCreated }: { open: boolean; state: State; onClose: () => void; onCreated: (id: string, warnings: string[]) => void }) {
  const landings = state.nodes.filter((n) => n.role === "landing");
  const [slot, setSlot] = useState<SlotInput>(emptySlot);
  const [mounts, setMounts] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [wasOpen, setWasOpen] = useState(false);

  // 每次打开时重置表单，并预选第一个网关和落地。
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) {
      setSlot({ ...emptySlot(), gateway: state.gateways[0]?.id ?? "", landing: landings[0]?.id ?? "" });
      setMounts("");
      setError("");
    }
  }

  const gateway = state.gateways.find((g) => g.id === slot.gateway);
  const sharing = state.slots.filter((s) => s.landing === slot.landing).length;
  const soloFull = !!gateway && !gateway.transit && gateway.slots.length > 0;

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const r = await api.addSlot({ ...slot, id: slot.id.trim(), mounts: mounts.split("\n").map((m) => m.trim()).filter(Boolean) });
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
          <Button onClick={save} disabled={saving || !slot.id.trim() || !slot.gateway || !slot.landing}>{saving ? "登记中…" : "登记"}</Button>
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
            <Line label="网关">
              <Select aria-label="网关" value={slot.gateway} onChange={(e) => setSlot({ ...slot, gateway: e.target.value })}>
                {state.gateways.map((g) => <option key={g.id} value={g.id}>{g.id}（{g.transit ? `中转 ${g.transit}` : "单跳直连落地"}，已有 {g.slots.length} 个槽位）</option>)}
              </Select>
            </Line>
            <Line label="落地">
              <Select aria-label="落地" value={slot.landing} onChange={(e) => setSlot({ ...slot, landing: e.target.value })}>
                {landings.map((n) => <option key={n.id} value={n.id}>{n.id}（{n.type}，{n.server}:{n.port}）</option>)}
              </Select>
            </Line>
            {(soloFull || sharing > 0) && (
              <p className="px-4 pb-3 pl-[116px] text-[11.5px] leading-[18px] text-warn">
                {soloFull && <span className="block">这个网关没有中转，只能接一个槽位，它已经有一个了。</span>}
                {sharing > 0 && <span className="block">这个落地已经绑定了 {sharing} 个槽位，它们的出口地址相同；同一个落地最多同时给 {state.landing_limit} 个槽位接线。</span>}
              </p>
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
          </div>
        </section>

        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
