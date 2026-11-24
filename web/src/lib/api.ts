// 经 HTTP 调用 cc-cluster 的 Go 接口（internal/web）。

const TOKEN_KEY = "cc-cluster-token";

/** 访问令牌来自地址里 # 之后的部分：读出来放进会话存储，再把它从地址栏去掉。 */
function readToken(): string {
  const m = /(?:^#|&)token=([0-9a-f]+)/.exec(window.location.hash);
  if (m) {
    try {
      sessionStorage.setItem(TOKEN_KEY, m[1]);
    } catch {
      /* 存不了就只在这次加载里用 */
    }
    history.replaceState(null, "", window.location.pathname + window.location.search);
    return m[1];
  }
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

const token = readToken();

/** 接口报错：message 是给用户看的说明，log 是出错之前的进度。 */
export class ApiError extends Error {
  log: string[];
  constructor(message: string, log: string[] = []) {
    super(message);
    this.log = log;
  }
}

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
    throw new ApiError("连不上 cc-cluster web：它可能已经停止，在终端里重新运行后用新打印的地址打开。");
  }
  let payload: { data?: T; error?: string; log?: string[] };
  try {
    payload = await resp.json();
  } catch {
    throw new ApiError(`接口返回了无法识别的内容（HTTP ${resp.status}）`);
  }
  if (!resp.ok || payload.error) throw new ApiError(payload.error ?? `HTTP ${resp.status}`, payload.log ?? []);
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
}

export interface Sub {
  id: string;
  note?: string;
  count: number;
  fetched_at: string;
  tested_at?: string;
  usable: number;
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
}

export interface Check {
  name: string;
  ok: boolean;
  detail: string;
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

  /** 设置备注；空串表示清掉。 */
  setNote: (kind: NoteKind, target: string, note: string) => post(`${kind}/${id(target)}/note`, { note }),

  addSub: (sub: string, url: string, note = "") => post<{ id: string; count: number }>("subs", { id: sub, url, note }),
  subNodes: (sub: string) => get<Delay[]>(`subs/${id(sub)}/nodes`),
  refreshSub: (sub: string) => post<{ Total: number; Updated: string[] | null; Renamed: string[] | null; Missing: string[] | null }>(`subs/${id(sub)}/refresh`),
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
