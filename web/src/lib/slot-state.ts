// 槽位状态的判断与配色：各页共用，同一状态在各处的颜色一致。
import type { Slot } from "./api";

type HasState = Pick<Slot, "state">;

/** 已接线：有网络。配置待应用（pending）时仍按上次的配置在线。 */
export const isWired = (s: HasState) => s.state === "ready" || s.state === "pending";

/** worker 容器在运行：已接线，或在运行但未接线。 */
export const isRunning = (s: HasState) => isWired(s) || s.state === "offline";

/** 状态圆点的颜色：已接线为绿，手动断网为红，在运行但未接线为黄，其余为灰。 */
export const dotTone = (s: HasState) => (isWired(s) ? "ok" : s.state === "hold" ? "bad" : s.state === "offline" ? "warn" : "");
