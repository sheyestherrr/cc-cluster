// 经 HTTP 调用 cc-cluster 的 Go 接口（internal/web）。

const TOKEN_KEY = "cc-cluster-token";

/**
 * 访问令牌来自地址里 # 之后的部分：读出来记在这个浏览器里，再把它从地址栏去掉。
 * 令牌是固定的，记住之后直接打开地址就能进，不用每次都带着它。
 */
function readToken(): string {
  const m = /(?:^#|&)token=([0-9a-f]+)/.exec(window.location.hash);
  if (m) {
    try {
      localStorage.setItem(TOKEN_KEY, m[1]);
    } catch {
      /* 存不了就只在这次加载里用 */
    }
    history.replaceState(null, "", window.location.pathname + window.location.search);
    return m[1];
  }
  try {
    return localStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

const token = readToken();

/** 接口报错：message 是给用户看的说明，log 是出错之前的进度，status 是 HTTP 状态码（连不上时为 0）。 */
export class ApiError extends Error {
  log: string[];
  status: number;
  constructor(message: string, log: string[] = [], status = 0) {
    super(message);
    this.log = log;
    this.status = status;
  }
}

/** 这个错误是不是“没有令牌或令牌不对”。 */
export const isUnauthorized = (e: unknown) => e instanceof ApiError && e.status === 401;

export interface Result<T> {
  data: T;
  log: string[];
}

async function request<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
  let resp: Response;
  try {
    resp = await fetch(`./api/${path}`, {
      method,
      headers: { "X-CC-Token": token, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("连不上 cc-cluster：网页界面可能已经停止，在终端里执行 cc-cluster web 重新启动。");
  }
  let payload: { data?: T; error?: string; log?: string[] };
  try {
    payload = await resp.json();
  } catch {
    throw new ApiError(`接口返回了无法识别的内容（HTTP ${resp.status}）`, [], resp.status);
  }
  if (!resp.ok || payload.error) throw new ApiError(payload.error ?? `HTTP ${resp.status}`, payload.log ?? [], resp.status);
  return { data: payload.data as T, log: payload.log ?? [] };
}

const get = async <T>(path: string) => (await request<T>("GET", path)).data;
const post = <T = null>(path: string, body: unknown = {}) => request<T>("POST", path, body);
const del = (path: string) => request<null>("DELETE", path);
const id = encodeURIComponent;

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export interface Mount {
  source: string;
  target: string;
  read_only?: boolean;
}

export type SlotState = "hold" | "absent" | "stopped" | "offline" | "pending" | "ready" | "unknown";

export interface Slot {
  id: string;
  /** 备注：给人看的名字或说明，只用于显示。 */
  note?: string;
  gateway: string;
  landing: string;
  state: SlotState;
  hold_reason?: string;
  /** 持续检测最近一次的结果。 */
  watch?: { at: string; since: string; status: "ok" | "unreachable" | "changed"; expect?: string; exit_ip?: string; detail?: string };
  exit_ip?: string;
  exit_where?: string;
  applied_at?: string;
  hostname: string;
  user: string;
  home: string;
  tz?: string;
  mounts: Mount[];
  memory_bytes?: number;
  cpus?: number;
  fake_dmi?: boolean;
  fake_proc?: boolean;
  tmux_bridge?: boolean;
  tmux_alive?: boolean;
  enter_command: string;
}

export interface Gateway {
  id: string;
  note?: string;
  transit?: string;
  /** 候选中转，按顺序；主用的不通时核心自动改用。 */
  transit_backups?: string[];
  /** 有候选时当前在用的中转；问不到时没有。 */
  active_transit?: string;
  egress_network: string;
  pool: string;
  container: string;
  core_ready: boolean;
  config_current: boolean;
  slots: string[];
}

export type Role = "transit" | "landing";

/** 一项不利标记：by 是命中它的数据来源，of 是对这一项给出了判断的来源数。 */
export interface ExitFlag {
  name: string;
  by: string[];
  of: number;
}

/** 第三方对落地出口 IP 的评估，只用于展示。 */
export interface ExitReport {
  ip: string;
  checked_at: string;
  sources: string[];
  country?: string;
  country_code?: string;
  region?: string;
  city?: string;
  timezone?: string;
  asn?: number;
  as_org?: string;
  asn_kind?: string;
  company?: string;
  company_type?: string;
  usage_type?: string;
  user_type?: string;
  connection_type?: string;
  residential: boolean;
  mobile: boolean;
  abuser_score?: string;
  trust_score?: number;
  fraud_score?: number;
  static_ip_score?: number;
  user_count?: number;
  flags: ExitFlag[];
}

export interface NodeInfo {
  id: string;
  note?: string;
  /** 直出的出口：订阅里的节点直接当出口用，固定给一个槽位。 */
  direct?: boolean;
  role: Role;
  type: string;
  server: string;
  port: number;
  connect_ipv4?: string;
  connect_auto?: boolean;
  fake_ip?: boolean;
  expect_exit_ip?: string;
  tz?: string;
  tz_auto?: boolean;
  tz_fallback?: boolean;
  exit_report?: ExitReport;
  source?: { sub: string; name: string };
  used_by: string[];
  /** 国家或地区代码（两位大写）；region_from 是 exit（出口评估）或 name（从名称里认出来的）。 */
  region?: string;
  region_from?: "exit" | "name";
}

export interface Sub {
  id: string;
  note?: string;
  count: number;
  fetched_at: string;
  tested_at?: string;
  usable: number;
  /** 本地订阅：内容是贴进来的，没有地址可拉取。 */
  local?: boolean;
  format?: "clash" | "links";
  /** 分享链接里没能解析的行数。 */
  skipped?: number;
  /** 自动刷新的间隔（分钟）；没有或 0 表示只手动刷新。 */
  every_min?: number;
  /** 最近一次尝试取订阅的时间；last_error 是那次失败的原因。 */
  checked_at?: string;
  last_error?: string;
}

export type SubSource = { url: string } | { content: string };

/** 刷新或修改订阅之后的结果（字段名是 Go 一侧原样给出的）。 */
export interface RefreshResult {
  Total: number;
  Skipped: number;
  Updated: string[] | null;
  Renamed: string[] | null;
  Missing: string[] | null;
}

export interface State {
  version: string;
  data_dir: string;
  cluster: { name: string; gateway_image: string; worker_image: string };
  exit_report_auto: boolean;
  docker_error?: string;
  landing_limit: number;
  gateways: Gateway[];
  slots: Slot[];
  nodes: NodeInfo[];
  subs: Sub[];
}

/** delay_ms：大于 0 是毫秒，0 是没测过，-1 是不通。 */
export interface Delay {
  name: string;
  type: string;
  server: string;
  delay_ms: number;
  info?: boolean;
  /** 从名称里认出的国家或地区代码。 */
  region?: string;
  /** 配置与它相同的已登记节点。 */
  nodes?: string[];
}

export interface Check {
  name: string;
  ok: boolean;
  detail: string;
}

/** 一个槽位此刻的流量：速率是最近两次采样之间的平均（字节每秒），up / down 是接线以来的累计字节。 */
export interface SlotLive {
  up_bps: number;
  down_bps: number;
  up: number;
  down: number;
  conns: number;
}

/** 各槽位此刻的流量。at 是最近一次采样的时间（还没采样过时是公元 1 年）；recording 表示这个进程在写历史。 */
export interface Traffic {
  at: string;
  recording: boolean;
  slots: Record<string, SlotLive>;
}

/** 一条当前的连接。看得到目标、流量和时长，看不到 HTTPS 的路径与内容。 */
export interface Conn {
  id: string;
  net: string;
  host: string;
  port: number;
  up: number;
  down: number;
  start: string;
  chain?: string;
}

/** 一档里的流量：up / down 是这一档里的字节，conns 是同时连接数的峰值。 */
export interface TrafficPoint {
  up: number;
  down: number;
  conns: number;
}

export interface HistoryPoint extends TrafficPoint {
  /** 这一档开始的时间。 */
  t: string;
  slots?: Record<string, TrafficPoint>;
}

export type WatchStatus = "ok" | "unreachable" | "changed";

/** 持续检测里状态没有变的一段。 */
export interface WatchSpan {
  from: string;
  to: string;
  status: WatchStatus;
  exit_ip?: string;
  checks: number;
}

export interface SubPoint {
  t: string;
  usable: number;
  total: number;
}

/** 一段时间里的趋势；step 是每一档的秒数。 */
export interface History {
  from: string;
  to: string;
  step: number;
  traffic: HistoryPoint[];
  totals: Record<string, TrafficPoint>;
  watch: Record<string, WatchSpan[]>;
  subs: Record<string, SubPoint[]>;
}

/** 一条已经结束的连接。 */
export interface ConnRecord {
  slot: string;
  start: string;
  end: string;
  net?: string;
  host: string;
  port?: number;
  up?: number;
  down?: number;
  chain?: string;
}

export interface HostStat {
  host: string;
  count: number;
  up: number;
  down: number;
  last: string;
}

/** 一个槽位的连接历史：最近结束的若干条（新的在前），和按流量排的目标。 */
export interface ConnLog {
  from: string;
  to: string;
  total: number;
  records: ConnRecord[];
  hosts: HostStat[];
}

export interface Op {
  time: string;
  op: string;
  target?: string;
  ok: boolean;
  error?: string;
  detail?: string;
}

export interface NodeInput {
  id: string;
  note?: string;
  role: Role;
  tz?: string;
  expect_exit_ip?: string;
  sub?: string;
  name?: string;
  type?: string;
  server?: string;
  port?: number;
  username?: string;
  password?: string;
}

export interface SlotInput {
  id: string;
  note?: string;
  gateway: string;
  landing: string;
  mounts: string[];
  memory: string;
  cpus: number;
  user: string;
  hostname: string;
  tz: string;
  fake_dmi: boolean;
  fake_proc: boolean;
  tmux_bridge: boolean;
  /** 直出：不用落地，直接用订阅里的这个节点当出口；给了它们就不用给网关和落地。 */
  direct_sub?: string;
  direct_name?: string;
}

/** 能设置备注的对象种类，取值就是接口地址里的那一段。 */
export type NoteKind = "subs" | "nodes" | "gateways" | "slots";

/** 对象名称（ID）的写法；备注不受这个限制。服务端按同样的规则检查。 */
export const ID_HINT = "小写字母、数字、连字符";
export const NOTE_MAX = 64;

export const api = {
  state: () => get<State>("state"),
  doctor: () => get<Check[]>("doctor"),
  ops: () => get<Op[]>("ops"),

  /** 各槽位此刻的流量（来自 cc-cluster web 进程里的采样）。 */
  traffic: () => get<Traffic>("traffic"),
  /** 最近 hours 小时的趋势：流量、持续检测的时间线、订阅测通的个数。 */
  history: (hours: number) => get<History>(`history?hours=${hours}`),
  slotConns: (slot: string) => get<Conn[]>(`slots/${id(slot)}/connections`),
  slotConnLog: (slot: string, hours: number, limit = 100) => get<ConnLog>(`slots/${id(slot)}/connlog?hours=${hours}&limit=${limit}`),

  /** 设置备注；空串表示清掉。 */
  setNote: (kind: NoteKind, target: string, note: string) => post(`${kind}/${id(target)}/note`, { note }),

  /** 添加订阅：source 里给 url 是远程订阅，给 content 是本地订阅（贴进来的分享链接、base64 订阅或 Clash 配置）。 */
  addSub: (sub: string, source: SubSource, note = "") => post<{ id: string; count: number; skipped: number }>("subs", { id: sub, note, ...source }),
  /** 修改订阅的来源；append 为真时把 content 接在本地订阅现有内容的后面。 */
  setSubSource: (sub: string, source: SubSource & { append?: boolean }) => post<RefreshResult>(`subs/${id(sub)}/source`, source),
  /** 设置远程订阅自动刷新的间隔（分钟）；0 表示不自动刷新。 */
  setSubEvery: (sub: string, minutes: number) => post(`subs/${id(sub)}/every`, { minutes }),
  subNodes: (sub: string) => get<Delay[]>(`subs/${id(sub)}/nodes`),
  refreshSub: (sub: string) => post<RefreshResult>(`subs/${id(sub)}/refresh`),
  testSub: (sub: string, landing: string) => post<Delay[]>(`subs/${id(sub)}/test`, { landing }),
  removeSub: (sub: string) => del(`subs/${id(sub)}`),

  addNode: (node: NodeInput) => post("nodes", node),
  resolveNode: (node: string) => post<{ connect_ipv4: string }>(`nodes/${id(node)}/resolve`),
  nodeReport: (node: string) => post<ExitReport>(`nodes/${id(node)}/report`),
  removeNode: (node: string) => del(`nodes/${id(node)}`),

  addGateway: (gw: { id: string; note?: string; transit: string; pool: string }) => post("gateways", gw),
  /** 更换网关的中转：第一个是主用的，其余是候选。 */
  setTransits: (gw: string, transits: string[], skipCheck = false) =>
    post<{ reloaded: boolean; rechecked: string[]; failed: Record<string, string> | null }>(`gateways/${id(gw)}/transits`, { transits, skip_check: skipCheck }),
  setExitReportAuto: (auto: boolean) => post("settings/exit-report", { auto }),
  setImages: (gateway_image: string, worker_image: string) => post("settings/images", { gateway_image, worker_image }),
  gatewayUp: (gw: string) => post(`gateways/${id(gw)}/up`),
  gatewayDown: (gw: string, force: boolean) => post(`gateways/${id(gw)}/down`, { force }),
  removeGateway: (gw: string) => del(`gateways/${id(gw)}`),

  addSlot: (slot: SlotInput) => post<{ warnings: string[] }>("slots", slot),
  slotUp: (slot: string, recreate: boolean) => post<{ exit: string }>(`slots/${id(slot)}/up`, { recreate }),
  slotStop: (slot: string) => post(`slots/${id(slot)}/stop`),
  slotHold: (slot: string, reason: string) => post(`slots/${id(slot)}/hold`, { reason }),
  slotRelease: (slot: string) => post(`slots/${id(slot)}/release`),
  removeSlot: (slot: string, purge: boolean) => del(`slots/${id(slot)}${purge ? "?purge=1" : ""}`),
};
