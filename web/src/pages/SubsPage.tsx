// 订阅页：左边是订阅，右边是所选订阅的状态和它的节点列表（筛选、测延迟、登记为节点）。
// 页面的骨架（列表 + 详情栏、分组表单）沿用 NetworkPage.tsx 的写法；“订阅表 + 节点表 + 筛选栏”的
// 信息安排参考了 Resin（MIT）的订阅页与节点池页，代码是按本项目的组件重新写的。
import { FileText, Gauge, Loader2, Pencil, Plus, RefreshCw, Rss, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Flag, RegionIcon, regionName } from "@/components/Flag";
import { NOTE_PLACEHOLDER, NoteDialog, type NoteTarget } from "@/components/NoteDialog";
import { DetailEmpty, DetailHeader, ErrorBox, KeyValue, Line, Page, PageHeader, Tag } from "@/components/PageHeader";
import { RowShell, type Selectable } from "@/components/RowShell";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { ApiError, ID_HINT, NOTE_MAX, api, errorText, type Delay, type RefreshResult, type Role, type State, type Sub } from "@/lib/api";
import { cn, relativeTime } from "@/lib/utils";

interface Props {
  state: State | null;
  loadError: string | null;
  refresh: () => Promise<void>;
}

type Failure = { text: string; log: string[] } | null;
type Editing = { mode: "add" } | { mode: "edit"; sub: Sub } | null;

const roleText: Record<Role, string> = { transit: "中转", landing: "落地" };

/** 自动刷新可选的间隔（分钟）。 */
const EVERY: [number, string][] = [[0, "不自动刷新"], [60, "每小时"], [360, "每 6 小时"], [720, "每 12 小时"], [1440, "每天"], [4320, "每 3 天"], [10080, "每周"]];

function everyText(minutes?: number): string {
  if (!minutes) return "";
  const known = EVERY.find(([m]) => m === minutes);
  if (known) return known[1];
  return minutes % 60 === 0 ? `每 ${minutes / 60} 小时` : `每 ${minutes} 分钟`;
}

function skippedText(n?: number): string {
  return n ? `，另有 ${n} 行没能认出来` : "";
}

/** 刷新或修改来源之后给用户的一句话。 */
function refreshText(id: string, d: RefreshResult): string {
  const parts = [`订阅 ${id} 现有 ${d.Total} 个节点${skippedText(d.Skipped)}`];
  if (d.Updated?.length) parts.push(`已更新节点 ${d.Updated.join("、")}（重新启动网关或槽位后生效）`);
  if (d.Renamed?.length) parts.push(`${d.Renamed.join("、")} 在订阅里改了名，已跟着改过来`);
  if (d.Missing?.length) parts.push(`订阅里已经没有 ${d.Missing.join("、")} 对应的节点，它们保持原样`);
  return parts.join("；");
}

export function SubsPage({ state, loadError, refresh }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [editing, setEditing] = useState<Editing>(null);
  const [removing, setRemoving] = useState<Sub | null>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  const [sel, setSel] = useState<string | null>(null);

  const run = async (id: string, label: string, action: () => Promise<unknown>, done?: string | ((r: unknown) => string)) => {
    setBusy((b) => ({ ...b, [id]: label }));
    setFailure(null);
    try {
      const r = await action();
      if (done) toast(typeof done === "string" ? done : done(r));
    } catch (e) {
      setFailure({ text: errorText(e), log: e instanceof ApiError ? e.log : [] });
    } finally {
      setBusy((b) => {
        const { [id]: _, ...rest } = b;
        return rest;
      });
      refresh();
    }
  };

  const reload = (s: Sub) =>
    run(s.id, s.local ? "解析中" : "刷新中", () => api.refreshSub(s.id), (r) => refreshText(s.id, (r as Awaited<ReturnType<typeof api.refreshSub>>).data));

  const subs = state?.subs ?? [];
  const current = subs.find((s) => s.id === sel) ?? subs[0];

  const side = current && state ? (
    <SubPanel
      key={current.id}
      s={current}
      state={state}
      busy={busy[current.id]}
      refresh={refresh}
      onReload={() => reload(current)}
      onEdit={() => setEditing({ mode: "edit", sub: current })}
      onEvery={(minutes) => run(current.id, "保存中", () => api.setSubEvery(current.id, minutes), minutes ? `订阅 ${current.id} ${everyText(minutes)}自动刷新一次` : `订阅 ${current.id} 不再自动刷新`)}
    />
  ) : (
    <DetailEmpty>{state ? "添加订阅后，这里列出它的节点：可以按地区和协议筛选、测延迟，再把选中的登记为中转或落地。" : "读取中…"}</DetailEmpty>
  );

  return (
    <Page side={side}>
      <PageHeader
        title="订阅"
        subtitle="订阅是一批现成的节点：可以是机场给的订阅地址，也可以是直接贴进来的分享链接。从里面挑出来登记的才是槽位用的节点。"
        actions={<Button size="sm" onClick={() => setEditing({ mode: "add" })}><Plus />添加订阅</Button>}
      />

      <div className="space-y-5 px-5">
        {loadError && <ErrorBox text={loadError} />}
        {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}

        {state && !subs.length && (
          <button onClick={() => setEditing({ mode: "add" })} className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed border-line px-4 py-10 text-muted hover:border-faint hover:text-fg active:!scale-100">
            <Rss className="h-5 w-5" />
            <span className="text-[13px] font-medium">还没有订阅</span>
            <span className="text-[12px]">填一个订阅地址，或者把 ss://、vmess://、vless://、trojan:// 这类分享链接直接贴进来</span>
          </button>
        )}

        {!!subs.length && (
          <section>
            <div className="group-label">订阅</div>
            <div className="list-card">
              {subs.map((s) => (
                <SubRow
                  key={s.id}
                  s={s}
                  selected={current?.id === s.id}
                  onSelect={() => setSel(s.id)}
                  busy={busy[s.id]}
                  onReload={() => reload(s)}
                  onEdit={() => setEditing({ mode: "edit", sub: s })}
                  onNote={() => setNoting({ kind: "subs", what: "订阅", id: s.id, note: s.note })}
                  onDelete={() => setRemoving(s)}
                />
              ))}
            </div>
          </section>
        )}
      </div>

      <SourceDialog
        editing={editing}
        onClose={() => setEditing(null)}
        onDone={async (id, text) => {
          setEditing(null);
          setSel(id);
          toast(text);
          await refresh();
        }}
      />
      <NoteDialog target={noting} onClose={() => setNoting(null)} onSaved={(text) => {
        setNoting(null);
        toast(text);
        void refresh();
      }} />
      <Dialog
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={`删除订阅 ${removing?.id ?? ""}？`}
        description="已经从它里面选用的节点不受影响，只是不再随订阅刷新。"
        footer={
          <>
            <Button variant="outline" onClick={() => setRemoving(null)}>取消</Button>
            <Button
              variant="destructive"
              onClick={() => {
                const s = removing!;
                setRemoving(null);
                run(s.id, "删除中", () => api.removeSub(s.id), `已删除订阅 ${s.id}`);
              }}
            >
              删除
            </Button>
          </>
        }
      />
    </Page>
  );
}

function SubRow({ s, busy, onReload, onEdit, onNote, onDelete, ...sel }: Selectable & { s: Sub; busy?: string; onReload: () => void; onEdit: () => void; onNote: () => void; onDelete: () => void }) {
  const tested = s.tested_at ? `${s.usable} 个测通（${relativeTime(s.tested_at)}测）` : "还没测过延迟";
  return (
    <RowShell
      {...sel}
      icon={s.local ? FileText : Rss}
      title={s.id}
      note={s.note}
      tags={
        <>
          <Tag>{s.local ? "本地" : "远程"}</Tag>
          {!!s.every_min && <Tag title="在 cc-cluster web 或 cc-cluster watch 运行期间自动刷新">{everyText(s.every_min)}</Tag>}
          {s.last_error && <Tag tone="danger" title={s.last_error}>刷新失败</Tag>}
          {!!s.skipped && <Tag tone="warn" title="协议不支持、写法不对，或者用了核心不支持的传输方式">{s.skipped} 行没认出</Tag>}
        </>
      }
      detail={`${s.count} 个节点 · ${tested} · ${relativeTime(s.fetched_at)}更新`}
      busy={busy}
      menu={[
        { label: s.local ? "重新解析" : "立即刷新", icon: RefreshCw, onSelect: onReload },
        { label: "修改来源…", icon: FileText, onSelect: onEdit },
        { label: "备注…", icon: Pencil, onSelect: onNote },
        "sep",
        { label: "删除…", icon: Trash2, danger: true, onSelect: onDelete },
      ]}
    />
  );
}

type Sort = "delay" | "name";

/** 详情栏：一个订阅的状态，和它的节点列表。 */
function SubPanel({ s, state, busy, refresh, onReload, onEdit, onEvery }: { s: Sub; state: State; busy?: string; refresh: () => Promise<void>; onReload: () => void; onEdit: () => void; onEvery: (minutes: number) => void }) {
  const landings = state.nodes.filter((n) => n.role === "landing");
  const used = state.nodes.filter((n) => n.source?.sub === s.id);
  const [nodes, setNodes] = useState<Delay[] | null>(null);
  const [error, setError] = useState("");
  const [via, setVia] = useState("");
  const [testedVia, setTestedVia] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [picked, setPicked] = useState<Delay | null>(null);

  const [query, setQuery] = useState("");
  const [region, setRegion] = useState("");
  const [type, setType] = useState("");
  const [sort, setSort] = useState<Sort>("delay");
  const [hideDead, setHideDead] = useState(true);
  const [hideInfo, setHideInfo] = useState(true);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(async () => {
    try {
      setNodes(await api.subNodes(s.id));
      setTestedVia(null);
      setError("");
    } catch (e) {
      setError(errorText(e));
    }
  }, [s.id]);

  // 订阅刷新过、测过延迟，或者登记的节点有变化时，重新读一遍。
  const registered = state.nodes.length;
  useEffect(() => {
    void load();
  }, [load, s.fetched_at, s.tested_at, registered]);

  const test = async () => {
    setTesting(true);
    setError("");
    try {
      const r = await api.testSub(s.id, via);
      setNodes(r.data);
      setTestedVia(via);
      if (!via) void refresh();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setTesting(false);
    }
  };

  // 接口给的顺序是：测通的按延迟从低到高，然后是没测的、不通的；“按延迟”就是这个顺序。
  const regions = useMemo(() => tally((nodes ?? []).filter((d) => !d.info).map((d) => d.region ?? "")), [nodes]);
  const types = useMemo(() => tally((nodes ?? []).map((d) => d.type)), [nodes]);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (nodes ?? []).filter(
      (d) =>
        (!hideDead || d.delay_ms >= 0) &&
        (!hideInfo || !d.info) &&
        (!region || (region === "?" ? !d.region : d.region === region)) &&
        (!type || d.type === type) &&
        (!q || d.name.toLowerCase().includes(q) || d.server.toLowerCase().includes(q) || (d.nodes ?? []).some((n) => n.includes(q))),
    );
    if (sort === "name") list.sort((a, b) => a.name.localeCompare(b.name, "zh-CN"));
    return list;
  }, [nodes, query, region, type, sort, hideDead, hideInfo]);

  const fastest = nodes?.find((d) => d.delay_ms > 0 && !d.info);
  const dead = nodes?.filter((d) => d.delay_ms < 0).length ?? 0;
  const infos = nodes?.filter((d) => d.info).length ?? 0;
  const LIMIT = 150;
  const visible = showAll ? shown : shown.slice(0, LIMIT);
  const everyOptions = EVERY.some(([m]) => m === (s.every_min ?? 0)) ? EVERY : [...EVERY, [s.every_min!, everyText(s.every_min)] as [number, string]];

  return (
    <>
      <DetailHeader
        badge={<RegionIcon icon={s.local ? FileText : Rss} size="detail" />}
        title={s.id}
        note={s.note}
        status={<span className="truncate">{s.local ? "本地订阅" : "远程订阅"} · {s.count} 个节点{s.format === "links" ? " · 分享链接" : s.format === "clash" ? " · Clash 格式" : ""}</span>}
        actions={
          busy ? (
            <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted"><Loader2 className="h-3.5 w-3.5 animate-spin" />{busy}</span>
          ) : (
            <>
              {!s.local && <Button variant="outline" size="sm" onClick={onReload}><RefreshCw />刷新</Button>}
              <Button variant="outline" size="sm" onClick={onEdit}>{s.local ? "添加或替换节点…" : "换地址…"}</Button>
            </>
          )
        }
      />

      <div className="space-y-5">
        {s.last_error && (
          <ErrorBox text={`${relativeTime(s.checked_at)}刷新失败：${s.last_error}。下面是${relativeTime(s.fetched_at)}成功取到的节点。`} />
        )}

        <section>
          <div className="group-label">状态</div>
          <div className="list-card">
            <KeyValue k="更新" v={`${relativeTime(s.fetched_at)}${s.skipped ? `；有 ${s.skipped} 行没能认出来（协议不支持、写法不对，或传输方式核心不支持）` : ""}`} plain />
            <KeyValue k="延迟" v={s.tested_at ? `${s.usable} 个测通（${relativeTime(s.tested_at)}测）` : "还没测过"} plain />
            <KeyValue k="已选用" v={used.length ? used.map((n) => `${n.id}（${n.source!.name}）`).join("、") : "还没有从它里面选用节点"} plain />
            {!s.local && (
              <Line label="自动刷新">
                <Select aria-label="自动刷新" value={s.every_min ?? 0} disabled={!!busy} onChange={(e) => onEvery(Number(e.target.value))}>
                  {everyOptions.map(([m, label]) => <option key={m} value={m}>{label}</option>)}
                </Select>
              </Line>
            )}
          </div>
          {!s.local && <p className="mt-1.5 px-1 text-[11.5px] leading-5 text-muted">自动刷新在这个页面所在的 cc-cluster web（或 cc-cluster watch）运行期间进行，跟着持续检测的节奏。已登记的节点会跟着订阅更新，重新启动网关或槽位后生效。</p>}
        </section>

        <section>
          <div className="group-label">节点</div>
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Select aria-label="测速方式" value={via} disabled={testing} onChange={(e) => setVia(e.target.value)}>
                <option value="">只测节点本身</option>
                {landings.map((n) => <option key={n.id} value={n.id}>接上落地 {n.id} 测整条链路</option>)}
              </Select>
              <Button variant="outline" onClick={test} disabled={testing || !nodes} className="shrink-0">
                {testing ? <Loader2 className="animate-spin" /> : <Gauge />}
                {testing ? "测速中…" : "全部测延迟"}
              </Button>
            </div>
            <p className="text-[11.5px] leading-5 text-muted">
              {testedVia === null
                ? "节点自己能用，不代表它肯转发到你的落地。挑中转时选一个落地再测，测的就是“节点 → 落地”的整条链路。"
                : testedVia
                  ? `下面是经节点再到落地 ${testedVia} 的延迟，只在这里显示，不保存。`
                  : "下面是节点自身的延迟，已保存在订阅上。"}
            </p>

            <div className="flex flex-wrap items-center gap-2">
              <Input aria-label="搜索节点" placeholder="搜索名称、服务器或已登记的节点" value={query} onChange={(e) => setQuery(e.target.value)} className="min-w-[160px] flex-1" />
              <Select aria-label="地区" value={region} onChange={(e) => setRegion(e.target.value)} className="w-auto">
                <option value="">全部地区</option>
                {regions.filter(([code]) => code).map(([code, n]) => <option key={code} value={code}>{regionName(code)}（{n}）</option>)}
                {regions.some(([code]) => !code) && <option value="?">认不出地区（{regions.find(([code]) => !code)![1]}）</option>}
              </Select>
              {types.length > 1 && (
                <Select aria-label="协议" value={type} onChange={(e) => setType(e.target.value)} className="w-auto">
                  <option value="">全部协议</option>
                  {types.map(([t, n]) => <option key={t} value={t}>{t}（{n}）</option>)}
                </Select>
              )}
              <Select aria-label="排序" value={sort} onChange={(e) => setSort(e.target.value as Sort)} className="w-auto">
                <option value="delay">按延迟</option>
                <option value="name">按名称</option>
              </Select>
            </div>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-fg-2">
              <label className="flex cursor-pointer items-center gap-1.5">
                <input type="checkbox" checked={hideDead} onChange={(e) => setHideDead(e.target.checked)} />
                隐藏不通的{dead ? `（${dead}）` : ""}
              </label>
              <label className="flex cursor-pointer items-center gap-1.5" title="订阅服务常把剩余流量、到期时间做成节点塞在列表里">
                <input type="checkbox" checked={hideInfo} onChange={(e) => setHideInfo(e.target.checked)} />
                隐藏提示项{infos ? `（${infos}）` : ""}
              </label>
              {nodes && <span className="ml-auto text-muted">显示 {shown.length} / {nodes.length} 个</span>}
            </div>

            {error && <ErrorBox text={error} />}
            {nodes === null && !error && <div className="py-6 text-center text-[12.5px] text-muted">读取中…</div>}
            {nodes && !shown.length && <div className="py-6 text-center text-[12.5px] text-muted">没有符合条件的节点</div>}
            {!!visible.length && (
              <div className="list-card">
                {visible.map((d, i) => (
                  <div key={`${d.name}-${i}`} className={cn("flex items-center gap-3 px-4 py-2 hover:bg-card-2", d.info && "opacity-60")}>
                    <span className="flex w-[20px] shrink-0 justify-center">
                      <Flag code={d.region} title={d.region ? `${regionName(d.region)}（从名称里认出来的）` : undefined} />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="selectable truncate text-[13px]">{d.name}</span>
                        {d === fastest && <Tag tone="ok">最快</Tag>}
                        {d.info && <Tag title="订阅里的提示项，不是给人用的线路">提示项</Tag>}
                        {d.nodes?.map((n) => <Tag key={n} tone="env" title={`已经登记为节点 ${n}`}>{n}</Tag>)}
                      </div>
                      <div className="selectable truncate font-mono text-[11px] text-muted">{d.type} · {d.server}</div>
                    </div>
                    <span className="w-14 shrink-0 text-right font-mono text-[11.5px]">{delayLabel(d)}</span>
                    <Button variant={d === fastest && !d.nodes?.length ? "default" : "outline"} size="sm" disabled={d.info} onClick={() => setPicked(d)}>登记</Button>
                  </div>
                ))}
                {shown.length > visible.length && (
                  <button type="button" className="w-full px-4 py-2 text-left text-[11.5px] text-muted hover:bg-card-2" onClick={() => setShowAll(true)}>
                    还有 {shown.length - visible.length} 个没有列出；点这里全部显示，或者用上面的筛选缩小范围
                  </button>
                )}
              </div>
            )}
          </div>
        </section>
      </div>

      <RegisterDialog sub={s.id} node={picked} onClose={() => setPicked(null)} onDone={refresh} />
    </>
  );
}

/** 统计各个取值出现的次数，按次数从多到少排。 */
function tally(values: string[]): [string, number][] {
  const count = new Map<string, number>();
  for (const v of values) count.set(v, (count.get(v) ?? 0) + 1);
  return [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function delayLabel(d: Delay) {
  if (d.delay_ms > 0) return <span className="text-ok">{d.delay_ms} ms</span>;
  if (d.delay_ms < 0) return <span className="text-danger">不通</span>;
  return <span className="text-faint">未测</span>;
}

/** 把订阅里的一个节点登记为中转或落地。 */
function RegisterDialog({ sub, node, onClose, onDone }: { sub: string; node: Delay | null; onClose: () => void; onDone: () => Promise<void> }) {
  const toast = useToast();
  const [form, setForm] = useState<{ id: string; note: string; role: Role; tz: string }>({ id: "", note: "", role: "transit", tz: "" });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const name = node?.name;
  useEffect(() => {
    if (!name) return;
    setForm({ id: "", note: "", role: "transit", tz: "" });
    setError("");
  }, [name]);

  const add = async () => {
    if (!node) return;
    setSaving(true);
    setError("");
    try {
      await api.addNode({ id: form.id.trim(), note: form.note.trim(), role: form.role, tz: form.role === "landing" ? form.tz.trim() : "", sub, name: node.name });
      toast(`已登记节点 ${form.id.trim()}（${roleText[form.role]}）。`);
      onClose();
      await onDone();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={!!node}
      onClose={onClose}
      title="登记为节点"
      description={<>把 <span className="font-medium text-fg">{node?.name}</span> 登记成槽位能用的节点。它以后跟着订阅 {sub} 一起更新。</>}
      className="max-w-[520px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={add} disabled={saving || !form.id.trim()}>{saving ? "登记中…" : "登记"}</Button>
        </>
      }
    >
      <div className="space-y-4">
        {!!node?.nodes?.length && <p className="text-[12px] leading-5 text-warn">它已经登记过了（{node.nodes.join("、")}）。再登记一次会得到另一个配置相同的节点。</p>}
        <div className="list-card">
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
        </div>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

const CONTENT_PLACEHOLDER = `一行一个分享链接，例如：
ss://…  vmess://…  vless://…  trojan://…
hysteria2://…  socks5://用户:密码@地址:端口  http://地址:端口

也可以贴 base64 订阅的内容，或者整份 Clash 配置（带 proxies 列表）。`;

/** 添加订阅，或修改一个订阅的来源。现有的地址和内容不显示：换地址就填新的，本地订阅可以往后追加或整体替换。 */
function SourceDialog({ editing, onClose, onDone }: { editing: Editing; onClose: () => void; onDone: (id: string, text: string) => void }) {
  const sub = editing?.mode === "edit" ? editing.sub : null;
  const [id, setId] = useState("");
  const [note, setNote] = useState("");
  const [kind, setKind] = useState<"url" | "content">("url");
  const [url, setUrl] = useState("");
  const [content, setContent] = useState("");
  const [append, setAppend] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const canAppend = !!sub?.local && sub.format === "links";

  const open = !!editing;
  const key = sub?.id ?? "";
  useEffect(() => {
    if (!open) return;
    setId("");
    setNote("");
    setUrl("");
    setContent("");
    setError("");
    setKind(sub?.local ? "content" : "url");
    setAppend(!!sub?.local && sub.format === "links");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, key]);

  const filled = kind === "url" ? !!url.trim() : !!content.trim();
  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const source = kind === "url" ? { url: url.trim() } : { content };
      if (sub) {
        const r = await api.setSubSource(sub.id, kind === "content" && canAppend && append ? { content, append: true } : source);
        onDone(sub.id, refreshText(sub.id, r.data));
      } else {
        const r = await api.addSub(id.trim(), source, note.trim());
        onDone(r.data.id, `已添加订阅 ${r.data.id}：${r.data.count} 个节点${skippedText(r.data.skipped)}。`);
      }
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
      title={sub ? `修改订阅 ${sub.id} 的来源` : "添加订阅"}
      description="订阅地址里通常带有令牌，分享链接里有节点的口令：它们只保存在这台机器的数据目录里，保存之后不再显示。"
      className="max-w-[600px] bg-bg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !filled || (!sub && !id.trim())}>{saving ? "读取中…" : sub ? "保存并读取" : "添加"}</Button>
        </>
      }
    >
      <div className="space-y-4">
        {!sub && (
          <div className="list-card">
            <Line label="名称">
              <Input aria-label="订阅名称" value={id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setId(e.target.value)} />
            </Line>
            <Line label="备注">
              <Input aria-label="订阅备注" value={note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setNote(e.target.value)} />
            </Line>
          </div>
        )}

        <div>
          <div className="mb-2 flex gap-1.5" role="tablist" aria-label="来源">
            <Button role="tab" aria-selected={kind === "url"} variant={kind === "url" ? "default" : "outline"} size="sm" onClick={() => setKind("url")}><Rss />订阅地址</Button>
            <Button role="tab" aria-selected={kind === "content"} variant={kind === "content" ? "default" : "outline"} size="sm" onClick={() => setKind("content")}><FileText />贴内容</Button>
          </div>
          {kind === "url" ? (
            <div className="list-card">
              <Line label={sub ? "新的地址" : "订阅地址"}>
                <Input aria-label="订阅地址" type="password" autoComplete="off" placeholder="https://…" value={url} onChange={(e) => setUrl(e.target.value)} />
              </Line>
            </div>
          ) : (
            <Textarea aria-label="订阅内容" rows={8} spellCheck={false} placeholder={CONTENT_PLACEHOLDER} value={content} onChange={(e) => setContent(e.target.value)} />
          )}
          {kind === "url" && <p className="mt-1.5 px-1 text-[11.5px] leading-5 text-muted">Clash（Mihomo）格式和通用的 base64 订阅都可以。由这台机器直接访问订阅服务器，不经过任何槽位的出口。{sub?.local && " 保存后这个订阅变成远程订阅，原来贴的内容不再保留。"}</p>}
          {kind === "content" && sub && (
            canAppend ? (
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-fg-2">
                <label className="flex cursor-pointer items-center gap-1.5"><input type="radio" name="append" checked={append} onChange={() => setAppend(true)} />接在现有内容后面</label>
                <label className="flex cursor-pointer items-center gap-1.5"><input type="radio" name="append" checked={!append} onChange={() => setAppend(false)} />替换全部内容</label>
              </div>
            ) : (
              <p className="mt-1.5 px-1 text-[11.5px] leading-5 text-muted">{sub.local ? "现有内容是 Clash 格式，不能往后追加：这里填的内容会替换全部。" : "保存后这个订阅变成本地订阅，不再从原来的地址拉取。"}</p>
            )
          )}
        </div>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
