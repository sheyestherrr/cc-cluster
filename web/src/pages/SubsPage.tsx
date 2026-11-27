// 订阅：一格一张卡片，点开是这个订阅的详情页——状态，和它的节点表（筛选、测延迟、登记为节点）。
// 版面与样式取自 Resin（MIT）webui 的订阅页与节点池页（卡片、筛选栏、数据表），内容是本项目的。
import { FileText, Gauge, Loader2, MoreHorizontal, Pencil, Plus, RefreshCw, Rss, Trash2 } from "lucide-react";
import type React from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Flag, RegionIcon, regionName } from "@/components/Flag";
import { Field, FormGrid } from "@/components/Form";
import { Menu, type MenuEntry } from "@/components/Menu";
import { NOTE_PLACEHOLDER, NoteDialog, type NoteTarget } from "@/components/NoteDialog";
import { ErrorBox, Page, PageHeader, Tabs, Tag } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input, Select, Textarea } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { ApiError, ID_HINT, NOTE_MAX, api, errorText, type Delay, type RefreshResult, type Role, type State, type Sub } from "@/lib/api";
import { relativeTime } from "@/lib/utils";

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
  return n ? `，另有 ${n} 行无法识别` : "";
}

/** 刷新或修改来源之后给用户的一句话。 */
function refreshText(id: string, d: RefreshResult): string {
  const parts = [`订阅 ${id} 共 ${d.Total} 个节点${skippedText(d.Skipped)}`];
  if (d.Updated?.length) parts.push(`已更新节点 ${d.Updated.join("、")}（重新启动网关或槽位后生效）`);
  if (d.Renamed?.length) parts.push(`${d.Renamed.join("、")} 在订阅中已改名，已同步更新`);
  if (d.Missing?.length) parts.push(`订阅中已无 ${d.Missing.join("、")} 对应的节点，这些节点保持不变`);
  return parts.join("；");
}

interface SubActions {
  reload: () => void;
  edit: () => void;
  note: () => void;
  remove: () => void;
  every: (minutes: number) => void;
}

function subMenu(s: Sub, a: SubActions): (MenuEntry | "sep")[] {
  return [
    { label: s.local ? "重新解析" : "立即刷新", icon: RefreshCw, onSelect: a.reload },
    { label: "修改来源…", icon: FileText, onSelect: a.edit },
    { label: "备注…", icon: Pencil, onSelect: a.note },
    "sep",
    { label: "删除…", icon: Trash2, danger: true, onSelect: a.remove },
  ];
}

function MoreMenu({ items, disabled }: { items: (MenuEntry | "sep")[]; disabled?: boolean }) {
  return (
    <Menu
      trigger={(toggle) => (
        <Button variant="ghost" size="icon" aria-label="更多" onClick={toggle} disabled={disabled}>
          <MoreHorizontal />
        </Button>
      )}
      items={items}
    />
  );
}

function SubBadges({ s }: { s: Sub }) {
  return (
    <>
      <Badge variant={s.local ? "neutral" : "info"}>{s.local ? "本地" : "远程"}</Badge>
      {!!s.every_min && <Badge variant="neutral" title="在 cc-cluster web 或 cc-cluster watch 运行期间自动刷新">{everyText(s.every_min)}</Badge>}
      {s.last_error && <Badge variant="danger" title={s.last_error}>刷新失败</Badge>}
      {!!s.skipped && <Badge variant="warning" title="协议不受支持、格式有误，或使用了核心不支持的传输方式">{s.skipped} 行无法识别</Badge>}
    </>
  );
}

export function SubsPage({ state, loadError, refresh }: Props) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<Failure>(null);
  const [editing, setEditing] = useState<Editing>(null);
  const [removing, setRemoving] = useState<Sub | null>(null);
  const [noting, setNoting] = useState<NoteTarget | null>(null);
  /** 打开着详情页的订阅；null 时显示卡片网格。 */
  const [open, setOpen] = useState<string | null>(null);

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

  const actions = (s: Sub): SubActions => ({
    reload: () => run(s.id, s.local ? "解析中" : "刷新中", () => api.refreshSub(s.id), (r) => refreshText(s.id, (r as Awaited<ReturnType<typeof api.refreshSub>>).data)),
    edit: () => setEditing({ mode: "edit", sub: s }),
    note: () => setNoting({ kind: "subs", what: "订阅", id: s.id, note: s.note }),
    remove: () => setRemoving(s),
    every: (minutes) => run(s.id, "保存中", () => api.setSubEvery(s.id, minutes), minutes ? `订阅 ${s.id} 已设为${everyText(minutes)}自动刷新` : `订阅 ${s.id} 的自动刷新已关闭`),
  });

  const subs = state?.subs ?? [];
  const current = open ? subs.find((s) => s.id === open) : undefined;

  const alerts = (
    <>
      {loadError && <ErrorBox text={loadError} />}
      {failure && <ErrorBox text={failure.text} log={failure.log} onClose={() => setFailure(null)} />}
    </>
  );

  const dialogs = (
    <>
      <SourceDialog
        editing={editing}
        onClose={() => setEditing(null)}
        onDone={async (id, text) => {
          setEditing(null);
          setOpen(id);
          toast(text);
          await refresh();
        }}
      />
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
        title={`删除订阅 ${removing?.id ?? ""}？`}
        description="已从该订阅添加的节点不受影响，但不再随订阅更新。"
        footer={
          <>
            <Button variant="outline" onClick={() => setRemoving(null)}>取消</Button>
            <Button
              variant="destructive"
              onClick={() => {
                const s = removing!;
                setRemoving(null);
                if (open === s.id) setOpen(null);
                run(s.id, "删除中", () => api.removeSub(s.id), `已删除订阅 ${s.id}`);
              }}
            >
              删除
            </Button>
          </>
        }
      />
    </>
  );

  if (state && current) {
    return (
      <>
        <SubDetail key={current.id} s={current} state={state} busy={busy[current.id]} a={actions(current)} alerts={alerts} refresh={refresh} onBack={() => setOpen(null)} />
        {dialogs}
      </>
    );
  }

  return (
    <Page className="platform-page">
      <PageHeader
        title="订阅"
        subtitle="管理远程与本地订阅，从中选择节点。"
        actions={<Button onClick={() => setEditing({ mode: "add" })}><Plus />添加订阅</Button>}
      />
      {alerts}

      <Card className="platform-cards-container">
        {!state && !loadError && <p className="muted">加载中…</p>}

        {state && !subs.length && (
          <div className="empty-box plain flex-col !gap-3 !py-12">
            <Rss size={20} />
            <p className="text-[15px] font-semibold text-fg">暂无订阅</p>
            <p className="max-w-[480px] text-center text-[13px]">填写订阅地址，或直接粘贴 ss://、vmess://、vless://、trojan:// 等分享链接。</p>
            <Button onClick={() => setEditing({ mode: "add" })}>添加订阅</Button>
          </div>
        )}

        {!!subs.length && (
          <div className="tile-grid">
            {subs.map((s) => <SubTile key={s.id} s={s} busy={busy[s.id]} a={actions(s)} onOpen={() => setOpen(s.id)} />)}
          </div>
        )}
      </Card>
      {dialogs}
    </Page>
  );
}

function SubTile({ s, busy, a, onOpen }: { s: Sub; busy?: string; a: SubActions; onOpen: () => void }) {
  return (
    <div
      className="tile"
      role="button"
      tabIndex={0}
      aria-label={`订阅 ${s.id}，打开详情`}
      onClick={(e) => !(e.target as HTMLElement).closest("button, [role=menu]") && onOpen()}
      onKeyDown={(e) => e.target === e.currentTarget && (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onOpen())}
    >
      <div className="tile-head">
        <RegionIcon icon={s.local ? FileText : Rss} />
        <div className="tile-title">
          <p title={s.note}>{s.id}{s.note && <span className="ml-2 font-sans text-[12px] font-normal text-muted">{s.note}</span>}</p>
          <p>{relativeTime(s.fetched_at)}更新</p>
        </div>
        <MoreMenu items={subMenu(s, a)} disabled={!!busy} />
      </div>

      <div className="tile-figures">
        <div className="tile-figure"><span>节点</span><strong className="num">{s.count}</strong></div>
        <div className="tile-figure"><span>可用</span><strong className="num">{s.tested_at ? s.usable : "—"}</strong></div>
        <div className="tile-figure"><span>上次测试</span><strong>{s.tested_at ? relativeTime(s.tested_at) : "未测试"}</strong></div>
      </div>

      <div className="platform-tile-foot">
        <div className="platform-tile-facts min-w-0 flex-1"><SubBadges s={s} /></div>
        {busy ? (
          <Button variant="outline" size="sm" disabled><Loader2 className="spin" />{busy}</Button>
        ) : (
          <Button variant="outline" size="sm" onClick={a.reload}><RefreshCw />{s.local ? "重新解析" : "刷新"}</Button>
        )}
      </div>
    </div>
  );
}

type Sort = "delay" | "name";

/** 详情页：一个订阅的状态，和它的节点表。 */
function SubDetail({ s, state, busy, a, alerts, refresh, onBack }: { s: Sub; state: State; busy?: string; a: SubActions; alerts: React.ReactNode; refresh: () => Promise<void>; onBack: () => void }) {
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
    if (sort === "name") list.sort((x, y) => x.name.localeCompare(y.name, "zh-CN"));
    return list;
  }, [nodes, query, region, type, sort, hideDead, hideInfo]);

  const fastest = nodes?.find((d) => d.delay_ms > 0 && !d.info);
  const dead = nodes?.filter((d) => d.delay_ms < 0).length ?? 0;
  const infos = nodes?.filter((d) => d.info).length ?? 0;
  const LIMIT = 150;
  const visible = showAll ? shown : shown.slice(0, LIMIT);
  const everyOptions = EVERY.some(([m]) => m === (s.every_min ?? 0)) ? EVERY : [...EVERY, [s.every_min!, everyText(s.every_min)] as [number, string]];
  const format = s.format === "links" ? "分享链接" : s.format === "clash" ? "Clash 格式" : "";

  return (
    <Page className="platform-page platform-detail-page">
      <PageHeader
        back={{ label: "全部订阅", onClick: onBack }}
        title={s.id}
        subtitle={s.note || "筛选节点、测试延迟，并将选中的添加为节点。"}
        actions={
          <>
            {busy ? (
              <Button variant="outline" disabled><Loader2 className="spin" />{busy}</Button>
            ) : (
              <>
                {!s.local && <Button variant="outline" onClick={a.reload}><RefreshCw />刷新</Button>}
                <Button variant="outline" onClick={a.edit}><FileText />{s.local ? "添加或替换节点" : "更换地址"}</Button>
              </>
            )}
            <MoreMenu items={subMenu(s, a)} disabled={!!busy} />
          </>
        }
      />
      {alerts}
      {s.last_error && <ErrorBox text={`${relativeTime(s.checked_at)}刷新失败：${s.last_error}。以下为${relativeTime(s.fetched_at)}获取的节点。`} />}

      <Card className="platform-directory-card platform-detail-header-card">
        <div className="platform-detail-header-main">
          <div className="flex min-w-0 items-center gap-3">
            <RegionIcon icon={s.local ? FileText : Rss} size="detail" />
            <div className="min-w-0">
              <h3 className="truncate">{s.count} 个节点</h3>
              <p>{s.local ? "本地订阅" : "远程订阅"}{format && ` · ${format}`} · {relativeTime(s.fetched_at)}更新{s.skipped ? ` · ${s.skipped} 行无法识别` : ""}</p>
            </div>
          </div>
          <div className="platform-detail-header-meta">
            <div className="flex flex-wrap justify-end gap-1.5"><SubBadges s={s} /></div>
            <span>{s.tested_at ? `${s.usable} 个可用，${relativeTime(s.tested_at)}测试` : "尚未测试延迟"}</span>
          </div>
        </div>
        <div className="platform-detail-header-footer">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
            <div className="min-w-0 flex-1 text-[13px] text-muted">
              已添加的节点：<span className="font-semibold text-fg">{used.length ? used.map((n) => `${n.id}（${n.source!.name}）`).join("、") : "暂无"}</span>
            </div>
            {!s.local && (
              <label className="flex shrink-0 items-center gap-2 text-[13px] text-muted" title="仅在 cc-cluster web 或 cc-cluster watch 运行期间执行。已添加的节点随订阅更新，重新启动网关或槽位后生效。">
                自动刷新
                <Select aria-label="自动刷新" className="field-sm !w-auto" value={s.every_min ?? 0} disabled={!!busy} onChange={(e) => a.every(Number(e.target.value))}>
                  {everyOptions.map(([m, label]) => <option key={m} value={m}>{label}</option>)}
                </Select>
              </label>
            )}
          </div>
        </div>
      </Card>

      <Card className="platform-cards-container flex flex-col gap-3">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="platform-drawer-section-head min-w-[260px] flex-1">
            <h4>节点</h4>
            <p>
              {testedVia === null
                ? "节点自身可用，不代表能转发到落地。选择中转时，请先指定落地再测试，以验证“节点 → 落地”的整条链路。"
                : testedVia
                  ? `以下为经节点到落地 ${testedVia} 的延迟，仅在此处显示，不保存。`
                  : "以下为节点自身的延迟，已随订阅保存。"}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Select aria-label="测试方式" className="!w-auto" value={via} disabled={testing} onChange={(e) => setVia(e.target.value)}>
              <option value="">仅测试节点自身</option>
              {landings.map((n) => <option key={n.id} value={n.id}>经落地 {n.id} 测试整条链路</option>)}
            </Select>
            <Button onClick={test} disabled={testing || !nodes}>
              {testing ? <Loader2 className="spin" /> : <Gauge />}
              {testing ? "测试中…" : "测试全部节点"}
            </Button>
          </div>
        </div>

        <div className="toolbar">
          <Input aria-label="搜索节点" placeholder="搜索名称、服务器或已添加的节点" value={query} onChange={(e) => setQuery(e.target.value)} className="!w-auto min-w-[200px] max-w-[320px] flex-1" />
          <Select aria-label="地区" value={region} onChange={(e) => setRegion(e.target.value)} className="!w-auto">
            <option value="">全部地区</option>
            {regions.filter(([code]) => code).map(([code, n]) => <option key={code} value={code}>{regionName(code)}（{n}）</option>)}
            {regions.some(([code]) => !code) && <option value="?">未识别地区（{regions.find(([code]) => !code)![1]}）</option>}
          </Select>
          {types.length > 1 && (
            <Select aria-label="协议" value={type} onChange={(e) => setType(e.target.value)} className="!w-auto">
              <option value="">全部协议</option>
              {types.map(([t, n]) => <option key={t} value={t}>{t}（{n}）</option>)}
            </Select>
          )}
          <Select aria-label="排序" value={sort} onChange={(e) => setSort(e.target.value as Sort)} className="!w-auto">
            <option value="delay">按延迟</option>
            <option value="name">按名称</option>
          </Select>
          <label className="flex cursor-pointer items-center gap-1.5 text-[13px] text-fg-2">
            <input type="checkbox" checked={hideDead} onChange={(e) => setHideDead(e.target.checked)} />
            隐藏不可用{dead ? `（${dead}）` : ""}
          </label>
          <label className="flex cursor-pointer items-center gap-1.5 text-[13px] text-fg-2" title="订阅服务常以节点的形式在列表中附带剩余流量、到期时间等信息">
            <input type="checkbox" checked={hideInfo} onChange={(e) => setHideInfo(e.target.checked)} />
            隐藏提示项{infos ? `（${infos}）` : ""}
          </label>
          <span className="grow" />
          {nodes && <span className="text-[12px] text-muted">显示 {shown.length} / {nodes.length} 个</span>}
        </div>

        {error && <ErrorBox text={error} />}
        {nodes === null && !error && <div className="empty-box plain"><p>加载中…</p></div>}
        {nodes && !shown.length && <div className="empty-box plain"><p>无符合条件的节点</p></div>}
        {!!visible.length && (
          <div className="data-table-wrap !mt-0 !max-h-none">
            <table className="data-table compact">
              <thead>
                <tr>
                  <th>节点</th>
                  <th>地区</th>
                  <th>协议</th>
                  <th>服务器</th>
                  <th>已添加为</th>
                  <th className="right">延迟</th>
                  <th className="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((d, i) => (
                  <tr key={`${d.name}-${i}`} className={d.info ? "!cursor-default opacity-60" : "!cursor-default"}>
                    <td>
                      <span className="flex items-center gap-1.5">
                        <span className="cell-main">{d.name}</span>
                        {d === fastest && <Tag tone="ok">最快</Tag>}
                        {d.info && <Tag title="订阅中的提示信息，不是可用的线路">提示项</Tag>}
                      </span>
                    </td>
                    <td>
                      {d.region ? (
                        <span className="flex items-center gap-1.5 whitespace-nowrap" title="根据名称识别，不代表实际出口">
                          <Flag code={d.region} />
                          {regionName(d.region)}
                        </span>
                      ) : <span className="text-faint">—</span>}
                    </td>
                    <td className="mono whitespace-nowrap">{d.type}</td>
                    <td className="mono cell-sub break-all">{d.server}</td>
                    <td>
                      {d.nodes?.length
                        ? <span className="flex flex-wrap gap-1">{d.nodes.map((n) => <Tag key={n} tone="env" title={`已添加为节点 ${n}`}>{n}</Tag>)}</span>
                        : <span className="text-faint">—</span>}
                    </td>
                    <td className="right mono whitespace-nowrap">{delayLabel(d)}</td>
                    <td className="right">
                      <Button variant={d === fastest && !d.nodes?.length ? "default" : "outline"} size="sm" disabled={d.info} onClick={() => setPicked(d)}>添加</Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {shown.length > visible.length && (
          <Button variant="ghost" className="self-start" onClick={() => setShowAll(true)}>
            另有 {shown.length - visible.length} 个未列出，显示全部
          </Button>
        )}
      </Card>

      <RegisterDialog sub={s.id} node={picked} onClose={() => setPicked(null)} onDone={refresh} />
    </Page>
  );
}

/** 统计各个取值出现的次数，按次数从多到少排。 */
function tally(values: string[]): [string, number][] {
  const count = new Map<string, number>();
  for (const v of values) count.set(v, (count.get(v) ?? 0) + 1);
  return [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function delayLabel(d: Delay) {
  if (d.delay_ms > 0) return <span className="font-semibold text-ok">{d.delay_ms} ms</span>;
  if (d.delay_ms < 0) return <span className="text-danger">不可用</span>;
  return <span className="text-faint">未测试</span>;
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
      toast(`已添加节点 ${form.id.trim()}（${roleText[form.role]}）。`);
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
      title="添加为节点"
      description={<>将 <span className="font-semibold text-fg">{node?.name}</span> 添加为槽位可使用的节点。该节点随订阅 {sub} 更新。</>}
      className="mid"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={add} disabled={saving || !form.id.trim()}>{saving ? "添加中…" : "添加"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!!node?.nodes?.length && <div className="callout callout-warning"><span>该节点已添加（{node.nodes.join("、")}）。再次添加将得到另一个配置相同的节点。</span></div>}
        <FormGrid>
          <Field label="名称">
            <Input aria-label="节点名称" value={form.id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setForm({ ...form, id: e.target.value })} />
          </Field>
          <Field label="备注">
            <Input aria-label="节点备注" value={form.note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setForm({ ...form, note: e.target.value })} />
          </Field>
          <Field label="角色" wide>
            <Select aria-label="角色" value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value as Role })}>
              <option value="transit">中转：多个槽位共用的第一跳</option>
              <option value="landing">落地：某个槽位的出口</option>
            </Select>
          </Field>
          {form.role === "landing" && (
            <Field label="时区" wide hint="留空则首次启动槽位时按出口所在地设置。">
              <Input aria-label="时区" placeholder="例如 America/Los_Angeles" value={form.tz} onChange={(e) => setForm({ ...form, tz: e.target.value })} />
            </Field>
          )}
        </FormGrid>
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}

const CONTENT_PLACEHOLDER = `每行一个分享链接，例如：
ss://…  vmess://…  vless://…  trojan://…
hysteria2://…  socks5://用户:密码@地址:端口  http://地址:端口

也可粘贴 base64 订阅的内容，或完整的 Clash 配置（含 proxies 列表）。`;

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
      description="订阅地址通常包含令牌，分享链接包含节点密码。两者仅保存在本机的数据目录中，保存后不再显示。"
      className="mid"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving || !filled || (!sub && !id.trim())}>{saving ? "处理中…" : sub ? "保存并刷新" : "添加"}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!sub && (
          <FormGrid>
            <Field label="名称">
              <Input aria-label="订阅名称" value={id} maxLength={32} autoFocus placeholder={ID_HINT} onChange={(e) => setId(e.target.value)} />
            </Field>
            <Field label="备注">
              <Input aria-label="订阅备注" value={note} maxLength={NOTE_MAX} placeholder={NOTE_PLACEHOLDER} onChange={(e) => setNote(e.target.value)} />
            </Field>
          </FormGrid>
        )}

        <Tabs label="来源" value={kind} onChange={setKind} items={[["url", "订阅地址"], ["content", "粘贴内容"]]} />

        {kind === "url" ? (
          <Field label={sub ? "新地址" : "订阅地址"} hint={<>支持 Clash（Mihomo）格式与通用的 base64 订阅。由本机直接访问订阅服务器，不经过任何槽位的出口。{sub?.local && " 保存后该订阅转为远程订阅，原有内容不再保留。"}</>}>
            <Input aria-label="订阅地址" type="password" autoComplete="off" placeholder="https://…" value={url} onChange={(e) => setUrl(e.target.value)} />
          </Field>
        ) : (
          <Field
            label="订阅内容"
            hint={sub && !canAppend ? (sub.local ? "现有内容为 Clash 格式，不支持追加，此处填写的内容将替换全部内容。" : "保存后该订阅转为本地订阅，不再从原地址获取。") : undefined}
          >
            <Textarea aria-label="订阅内容" rows={8} spellCheck={false} placeholder={CONTENT_PLACEHOLDER} value={content} onChange={(e) => setContent(e.target.value)} />
            {sub && canAppend && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-[13px] text-fg-2">
                <label className="flex cursor-pointer items-center gap-1.5"><input type="radio" name="append" checked={append} onChange={() => setAppend(true)} />追加到现有内容之后</label>
                <label className="flex cursor-pointer items-center gap-1.5"><input type="radio" name="append" checked={!append} onChange={() => setAppend(false)} />替换全部内容</label>
              </div>
            )}
          </Field>
        )}
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
