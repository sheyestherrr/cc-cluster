// 系统：数据目录、出口评估的开关、运行环境检查与操作记录。
// 卡片、键值格与数据表的样式取自 Resin（MIT）webui 的样式表，内容是本项目的。
import { CheckCircle2, Loader2, Pencil, RefreshCw, Stethoscope, XCircle } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Field, FormGrid, SwitchItem } from "@/components/Form";
import { ErrorBox, Page, PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge, Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { api, errorText, type Check, type Op, type State } from "@/lib/api";
import { opDetail, opName } from "@/lib/ops";
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
  const [images, setImages] = useState<{ gateway: string; worker: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const toast = useToast();

  const saveImages = async () => {
    if (!images) return;
    setSaving(true);
    setError("");
    try {
      await api.setImages(images.gateway.trim(), images.worker.trim());
      await Promise.all([refresh(), loadOps()]);
      setImages(null);
      toast("镜像设置已更新，重建容器后生效");
    } catch (e) {
      setError(errorText(e));
      setImages(null);
    } finally {
      setSaving(false);
    }
  };

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
      <PageHeader title="系统" subtitle="集群信息、环境检查与操作记录。" />
      {error && <ErrorBox text={error} onClose={() => setError("")} />}

      <div className="grid items-stretch gap-3.5 min-[1100px]:grid-cols-2">
        <Card className="panel">
          <PanelHead title="数据" hint="数据目录与所用镜像">
            <Button variant="outline" size="sm" disabled={!state} onClick={() => state && setImages({ gateway: state.cluster.gateway_image, worker: state.cluster.worker_image })}>
              <Pencil />更换镜像
            </Button>
          </PanelHead>
          <div className="kv-grid">
            <KV k="版本" v={state?.version} />
            <KV k="容器名前缀" v={state?.cluster.name} />
            <KV k="数据目录" v={state?.data_dir} />
            <KV k="网关镜像" v={state?.cluster.gateway_image} />
            <KV k="worker 镜像" v={state?.cluster.worker_image} />
          </div>
          <SwitchItem
            label="启动槽位时查询出口评估"
            hint="经槽位的链路查询第三方接口，仅用于展示。"
            checked={state?.exit_report_auto ?? true}
            onChange={setExitReport}
          />
        </Card>

        <Card className="panel">
          <PanelHead title="运行环境" hint="检查 Docker、镜像与容器的网络能力">
            <Button variant="outline" size="sm" disabled={checking} onClick={doctor}>
              {checking ? <Loader2 className="spin" /> : <Stethoscope />}
              {checking ? "检查中…" : "运行检查"}
            </Button>
          </PanelHead>
          {!checks && <div className="empty-box plain"><p>{checking ? "检查中…" : "尚未检查。点击右上角的“运行检查”开始。"}</p></div>}
          {checks && (
            <>
              <div className={`callout ${failed ? "callout-error" : "callout-success"}`}>
                {failed ? <XCircle size={15} /> : <CheckCircle2 size={15} />}
                <span>{failed ? `${failed} 项未通过，共 ${checks.length} 项。` : `${checks.length} 项全部通过。`}</span>
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
        <PanelHead title="操作记录" hint="最近 40 条">
          <Button variant="outline" size="sm" onClick={loadOps}><RefreshCw />刷新</Button>
        </PanelHead>
        {ops === null && <div className="empty-box plain"><p>加载中…</p></div>}
        {ops?.length === 0 && <div className="empty-box plain"><p>暂无操作记录。</p></div>}
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
                    <td className="whitespace-nowrap" title={op.op}>{opName(op.op)}</td>
                    <td className="cell-main mono whitespace-nowrap">{op.target || "—"}</td>
                    <td><Badge variant={op.ok ? "success" : "danger"}>{op.ok ? "成功" : "失败"}</Badge></td>
                    <td className="cell-sub break-words">{op.ok ? opDetail(op.detail) : op.error}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Dialog
        open={!!images}
        onClose={() => setImages(null)}
        title="更换镜像"
        description="仅修改镜像设置。运行中的容器仍使用原镜像，将网关停止后重新启动、槽位重建 worker 后生效。镜像需已拉取到本机。"
        className="mid"
        footer={
          <>
            <Button variant="outline" onClick={() => setImages(null)}>取消</Button>
            <Button onClick={saveImages} disabled={saving || !images?.gateway.trim() || !images?.worker.trim()}>{saving ? "保存中…" : "保存"}</Button>
          </>
        }
      >
        <FormGrid>
          <Field label="网关镜像" wide>
            <Input aria-label="网关镜像" spellCheck={false} value={images?.gateway ?? ""} onChange={(e) => images && setImages({ ...images, gateway: e.target.value })} />
          </Field>
          <Field label="worker 镜像" wide hint="新建槽位的默认镜像；已单独指定镜像的槽位不变。">
            <Input aria-label="worker 镜像" spellCheck={false} value={images?.worker ?? ""} onChange={(e) => images && setImages({ ...images, worker: e.target.value })} />
          </Field>
        </FormGrid>
      </Dialog>

      <Card className="panel">
        <PanelHead title="致谢" hint="用到的开源项目" />
        <div className="text-[13px] leading-6 text-fg-2">
          <p>网页界面的版面、配色与样式取自 Resin（MIT 许可），分享链接的解析改写自该项目。</p>
          <p>构建配置与部分基础组件的行为沿用自 Claude-Hydra（MIT 许可）。</p>
          <p>网关核心是 Mihomo；默认 worker 镜像的软件包选择参考了 Claude Code 官方的开发容器。</p>
        </div>
      </Card>
    </Page>
  );
}
