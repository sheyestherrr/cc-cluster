// 操作记录里的操作名（slot.up 这类）换成中文；不认识的原样显示。
const names: Record<string, string> = {
  "slot.add": "新建槽位",
  "slot.up": "启动槽位",
  "slot.stop": "停止槽位",
  "slot.rm": "删除槽位",
  "slot.hold": "手动断网",
  "slot.release": "解除断网",
  "slot.mount": "挂载目录",
  "slot.unmount": "取消挂载",
  "slot.tmux": "设置宿主桥接",
  "gateway.add": "新建网关",
  "gateway.up": "启动网关",
  "gateway.down": "停止网关",
  "gateway.rm": "删除网关",
  "gateway.transit": "更换中转",
  "gateway.transits": "更换中转",
  "node.add": "添加节点",
  "node.rm": "删除节点",
  "node.resolve": "解析首跳地址",
  "node.report": "检测出口",
  "sub.add": "添加订阅",
  "sub.set": "修改订阅来源",
  "sub.every": "设置自动刷新",
  "sub.refresh": "刷新订阅",
  "sub.test": "测延迟",
  "sub.rm": "删除订阅",
  "tmux.start": "启动宿主 tmux",
  "tmux.kill": "关闭宿主 tmux",
  "cluster.exit-report": "设置出口评估",
  watch: "持续检测",
};

const kinds: Record<string, string> = { slot: "槽位", gateway: "网关", node: "节点", sub: "订阅" };
const details: Record<string, string> = { landing: "落地", transit: "中转" };

export function opName(op: string): string {
  if (names[op]) return names[op];
  // 改备注这类按“对象.动作”记的：slot.note → 槽位备注。
  const [kind, action] = op.split(".");
  if (action === "note" && kinds[kind.replace(/s$/, "")]) return `${kinds[kind.replace(/s$/, "")]}备注`;
  return op;
}

export function opDetail(text?: string): string {
  return text ? (details[text] ?? text) : "";
}
