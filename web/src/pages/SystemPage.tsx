// 改编自 Claude-Hydra（MIT）app/src/pages/SettingsPage.tsx 与 app/src/pages/AboutPage.tsx，v1.1.6：
// 沿用分组、键值行与主题切换的结构，内容换成 cc-cluster 的数据目录、体检与操作记录。
import { CheckCircle2, Loader2, Stethoscope, XCircle } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { ErrorBox, Group, KeyValue, Page, PageHeader, Row } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { api, errorText, type Check, type Op, type State } from "@/lib/api";
import type { ThemeMode } from "@/lib/theme";
import { clockTime, cn } from "@/lib/utils";

interface Props {
  state: State | null;
  refresh: () => Promise<void>;
  theme: ThemeMode;
  onThemeChange: (m: ThemeMode) => void;
}

const themes: { id: ThemeMode; label: string }[] = [
  { id: "system", label: "跟随系统" },
  { id: "light", label: "浅色" },
  { id: "dark", label: "深色" },
];

export function SystemPage({ state, refresh, theme, onThemeChange }: Props) {
  const [checks, setChecks] = useState<Check[] | null>(null);
  const [checking, setChecking] = useState(false);
  const [ops, setOps] = useState<Op[] | null>(null);
  const [error, setError] = useState("");

  const loadOps = useCallback(async () => {
    try {
      setOps(await api.ops());
    } catch (e) {
      setError(errorText(e));
    }
  }, []);
  useEffect(() => {
    void loadOps();
  }, [loadOps]);

  const setExitReport = async (auto: boolean) => {
    setError("");
    try {
      await api.setExitReportAuto(auto);
      await refresh();
    } catch (e) {
      setError(errorText(e));
    }
  };

  const doctor = async () => {
    setChecking(true);
    setError("");
    try {
      setChecks(await api.doctor());
    } catch (e) {
      setError(errorText(e));
    } finally {
      setChecking(false);
    }
  };

  return (
    <Page>
      <PageHeader title="系统" subtitle="外观、数据目录、运行环境检查与操作记录。" />
      <div className="space-y-5 px-5">
        {error && <ErrorBox text={error} onClose={() => setError("")} />}

        <Group label="外观">
          <Row label="主题" hint="跟随系统时会随系统的浅色 / 深色自动切换。">
            <div className="flex rounded-lg bg-pill p-0.5">
              {themes.map((t) => (
                <button
                  key={t.id}
                  onClick={() => onThemeChange(t.id)}
                  className={cn(
                    "h-6 rounded-md px-3 text-[12.5px] active:!scale-100",
                    theme === t.id ? "bg-card font-medium text-fg shadow-edge dark:bg-white/[.12]" : "text-muted hover:text-fg",
                  )}
                >
                  {t.label}
                </button>
              ))}
            </div>
          </Row>
        </Group>

        <Group label="数据">
          <div className="py-1">
            <KeyValue k="版本" v={state?.version} />
            <KeyValue k="数据目录" v={state?.data_dir} />
            <KeyValue k="容器名前缀" v={state?.cluster.name} />
            <KeyValue k="网关镜像" v={state?.cluster.gateway_image} />
            <KeyValue k="worker 镜像" v={state?.cluster.worker_image} />
          </div>
        </Group>

        <Group label="出口评估">
          <Row label="启动槽位时顺带查询" hint="经该槽的链路向第三方接口查一次出口 IP 的评估（机房 / 代理 / 滥用标记），只用于展示。关掉后不再自动查询，落地详情里的“重新查询”仍可手动查。">
            <Switch checked={state?.exit_report_auto ?? true} onCheckedChange={setExitReport} label="启动槽位时顺带查询出口评估" />
          </Row>
        </Group>

        <Group label="运行环境">
          <Row label="检查这台机器" hint="Docker 是否可用、镜像在不在、容器里能否建立网卡、写策略路由和打开 TUN 设备。不访问任何外部地址。">
            <Button variant="outline" size="sm" disabled={checking} onClick={doctor}>
              {checking ? <Loader2 className="animate-spin" /> : <Stethoscope />}
              {checking ? "检查中…" : "运行检查"}
            </Button>
          </Row>
          {checks?.map((c) => (
            <div key={c.name} className="flex items-start gap-2.5 px-4 py-2.5">
              {c.ok ? <CheckCircle2 className="mt-px h-4 w-4 shrink-0 text-ok" /> : <XCircle className="mt-px h-4 w-4 shrink-0 text-danger" />}
              <div className="min-w-0">
                <div className="text-[12.5px] font-medium">{c.name}</div>
                <div className="selectable break-words text-[11.5px] leading-[18px] text-muted">{c.detail}</div>
              </div>
            </div>
          ))}
        </Group>

        <Group label="操作记录" action={<button onClick={loadOps} className="text-[11.5px] text-muted hover:text-fg">刷新</button>}>
          {ops === null && <div className="px-4 py-3 text-[12.5px] text-muted">读取中…</div>}
          {ops?.length === 0 && <div className="px-4 py-3 text-[12.5px] text-muted">还没有操作记录。</div>}
          {ops?.slice(0, 40).map((op, i) => (
            <div key={`${op.time}-${i}`} className="flex items-start gap-3 px-4 py-2 text-[12px]">
              <span className={cn("mt-[5px] h-1.5 w-1.5 shrink-0 rounded-full", op.ok ? "bg-ok" : "bg-danger")} />
              <span className="w-[132px] shrink-0 font-mono text-[11px] text-muted">{clockTime(op.time)}</span>
              <span className="w-28 shrink-0 font-mono text-[11.5px] text-fg-2">{op.op}</span>
              <span className="selectable min-w-0 flex-1 break-words text-muted">
                {op.target && <span className="mr-1.5 text-fg-2">{op.target}</span>}
                {op.ok ? op.detail : op.error}
              </span>
            </div>
          ))}
        </Group>

        <Group label="致谢">
          <div className="px-4 py-3 text-[12px] leading-[19px] text-muted">
            <p>网页界面的风格、基础组件和页面骨架取自 Claude-Hydra（MIT 许可），订阅导入的做法也参考了它。</p>
            <p className="mt-1">网关核心是 Mihomo；默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器。</p>
          </div>
        </Group>
      </div>
    </Page>
  );
}
