// 系统：数据目录、出口评估的开关、运行环境检查与操作记录。
// 卡片、键值格与数据表的样式取自 Resin（MIT）webui 的样式表，内容是本项目的。
import { CheckCircle2, Loader2, RefreshCw, Stethoscope, XCircle } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { SwitchItem } from "@/components/Form";
import { ErrorBox, Page, PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { api, errorText, type Check, type Op, type State } from "@/lib/api";
import { clockTime } from "@/lib/utils";

interface Props {
  state: State | null;
  refresh: () => Promise<void>;
}

function KV({ k, v }: { k: string; v?: string }) {
  return (
    <div className="kv">
      <span>{k}</span>
      <strong className="mono">{v || "—"}</strong>
    </div>
  );
}

function PanelHead({ title, hint, children }: { title: string; hint: string; children?: React.ReactNode }) {
  return (
    <div className="panel-head">
      <div>
        <h3>{title}</h3>
        <p>{hint}</p>
      </div>
      {children}
    </div>
  );
}

export function SystemPage({ state, refresh }: Props) {
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

  const failed = checks?.filter((c) => !c.ok).length ?? 0;

  return (
    <Page>
      <PageHeader title="系统" subtitle="数据目录、运行环境检查与操作记录。" />
      {error && <ErrorBox text={error} onClose={() => setError("")} />}

      <div className="grid items-stretch gap-3.5 min-[1100px]:grid-cols-2">
        <Card className="panel">
          <PanelHead title="数据" hint="这个面板管理的集群，和它用的镜像" />
          <div className="kv-grid">
            <KV k="版本" v={state?.version} />
            <KV k="容器名前缀" v={state?.cluster.name} />
            <KV k="数据目录" v={state?.data_dir} />
            <KV k="网关镜像" v={state?.cluster.gateway_image} />
            <KV k="worker 镜像" v={state?.cluster.worker_image} />
          </div>
          <SwitchItem
            label="启动槽位时顺带查询出口评估"
            hint="经该槽的链路向第三方接口查一次出口 IP 的评估（机房 / 代理 / 滥用标记），只用于展示。关掉后不再自动查询，落地详情里的“重新检测”仍可手动查。"
            checked={state?.exit_report_auto ?? true}
            onChange={setExitReport}
          />
        </Card>

        <Card className="panel">
          <PanelHead title="运行环境" hint="Docker 是否可用、镜像在不在、容器里能否建立网卡、写策略路由和打开 TUN 设备；不访问任何外部地址">
            <Button variant="outline" size="sm" disabled={checking} onClick={doctor}>
              {checking ? <Loader2 className="spin" /> : <Stethoscope />}
              {checking ? "检查中…" : "运行检查"}
            </Button>
          </PanelHead>
          {!checks && <div className="empty-box plain"><p>{checking ? "检查中…" : "还没有检查过，点右上角的“运行检查”。"}</p></div>}
          {checks && (
            <>
              <div className={`callout ${failed ? "callout-error" : "callout-success"}`}>
                {failed ? <XCircle size={15} /> : <CheckCircle2 size={15} />}
                <span>{failed ? `${failed} 项没有通过，共 ${checks.length} 项。` : `${checks.length} 项全部通过。`}</span>
              </div>
              <div className="flex flex-col gap-2">
                {checks.map((c) => (
                  <div key={c.name} className="snapshot-row !items-start">
                    {c.ok ? <CheckCircle2 size={16} className="mt-0.5 shrink-0 text-ok" /> : <XCircle size={16} className="mt-0.5 shrink-0 text-danger" />}
                    <div className="min-w-0 flex-1">
                      <p className="font-semibold">{c.name}</p>
                      <p className="break-words text-[12px] text-muted">{c.detail}</p>
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}
        </Card>
      </div>

      <Card className="panel">
        <PanelHead title="操作记录" hint="最近 40 条：命令行和网页上做过的启动、停止、接线和修改">
          <Button variant="outline" size="sm" onClick={loadOps}><RefreshCw />刷新</Button>
        </PanelHead>
        {ops === null && <div className="empty-box plain"><p>读取中…</p></div>}
        {ops?.length === 0 && <div className="empty-box plain"><p>还没有操作记录。</p></div>}
        {!!ops?.length && (
          <div className="data-table-wrap !mt-0">
            <table className="data-table compact">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>操作</th>
                  <th>对象</th>
                  <th>结果</th>
                  <th>说明</th>
                </tr>
              </thead>
              <tbody>
                {ops.slice(0, 40).map((op, i) => (
                  <tr key={`${op.time}-${i}`} className="!cursor-default">
                    <td className="mono cell-sub whitespace-nowrap">{clockTime(op.time)}</td>
                    <td className="mono whitespace-nowrap">{op.op}</td>
                    <td className="cell-main whitespace-nowrap">{op.target || "—"}</td>
                    <td><Badge variant={op.ok ? "success" : "danger"}>{op.ok ? "成功" : "失败"}</Badge></td>
                    <td className="cell-sub break-words">{op.ok ? op.detail : op.error}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card className="panel">
        <PanelHead title="致谢" hint="这个界面站在这些项目的肩膀上" />
        <div className="text-[13px] leading-6 text-fg-2">
          <p>网页界面的版面、配色与样式取自 Resin（MIT 许可），分享链接的解析也改写自它。</p>
          <p>构建配置和几个基础组件的行为沿用自 Claude-Hydra（MIT 许可）。</p>
          <p>网关核心是 Mihomo；默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器。</p>
        </div>
      </Card>
    </Page>
  );
}
