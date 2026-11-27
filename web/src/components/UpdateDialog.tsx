// 版本与更新：当前版本、最新版本、发布日期，下面是发布页上写的更新说明。
// 页面只负责告诉使用者有新版本、该执行哪条命令；更新本身在终端里做。
import { ArrowUpCircle, CheckCircle2, ExternalLink, Info, RefreshCw, TriangleAlert } from "lucide-react";
import { CopyButton } from "@/components/CopyButton";
import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import type { Release, UpdateInfo } from "@/lib/api";
import { dateText, relativeTime } from "@/lib/utils";

interface Props {
  open: boolean;
  onClose: () => void;
  /** 页面加载时的版本；info 还没读到时先显示它。 */
  version?: string;
  info: UpdateInfo | null;
  checking: boolean;
  /** 接口本身请求失败（服务已停止之类）时的说明。 */
  error?: string;
  onCheck: () => void;
}

/** 发布日期；只查到版本号时接口给的是零值，显示成没有。 */
function publishedText(r?: Release): string {
  const ms = r ? Date.parse(r.published_at) : NaN;
  return Number.isNaN(ms) || ms < Date.UTC(2000, 0, 1) ? "" : dateText(r!.published_at);
}

/** 只有 http(s) 的地址才做成链接。 */
const safeHref = (href: string) => (/^https?:\/\//i.test(href) ? href : undefined);

function Fact({ k, v, strong }: { k: string; v?: string; strong?: boolean }) {
  return (
    <div className="kv">
      <span>{k}</span>
      <strong className={strong ? "mono text-[var(--primary-strong)]" : "mono"}>{v || "—"}</strong>
    </div>
  );
}

function Command({ text }: { text: string }) {
  return (
    <div className="flex items-stretch gap-2">
      <code className="code-line min-w-0 flex-1">{text}</code>
      <CopyButton text={text} variant="outline" size="sm" aria-label={`复制命令 ${text}`} />
    </div>
  );
}

export function UpdateDialog({ open, onClose, version, info, checking, error, onCheck }: Props) {
  const current = info?.current ?? version;
  const latest = info?.latest;
  const fromSource = !!info && !info.is_release;
  const problem = error || info?.error;

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="版本与更新"
      className="mid"
      footer={
        <>
          <span className="field-hint mr-auto self-center">
            {info?.checked_at ? `上次检查：${relativeTime(info.checked_at)}` : info ? "尚未检查" : ""}
          </span>
          <Button variant="outline" onClick={onCheck} disabled={checking}>
            <RefreshCw className={checking ? "animate-spin" : undefined} />
            {checking ? "检查中…" : "检查更新"}
          </Button>
          <Button onClick={onClose}>关闭</Button>
        </>
      }
    >
      <div className="grid gap-2.5 sm:grid-cols-3">
        <Fact k="当前版本" v={current} />
        <Fact k="最新版本" v={latest?.version} strong={info?.newer} />
        <Fact k="发布日期" v={publishedText(latest)} />
      </div>

      <div className="mt-3 grid gap-2.5">
        {info?.newer && latest && (
          <div className="callout callout-info">
            <ArrowUpCircle size={16} />
            <span>
              有新版本 <span className="mono">{latest.version}</span>。
            </span>
          </div>
        )}
        {info && !info.newer && latest && !fromSource && (
          <div className="callout callout-success">
            <CheckCircle2 size={16} />
            <span>已是最新版本。</span>
          </div>
        )}
        {fromSource && (
          <div className="callout callout-info">
            <Info size={16} />
            <span>当前为源码构建，不支持自动更新。请更新源码后重新构建。</span>
          </div>
        )}
        {problem && (
          <div className="callout callout-warning">
            <TriangleAlert size={16} />
            <span className="min-w-0 break-words">
              检查更新失败：{problem}
              {latest && "。以下为上次检查的结果。"}
            </span>
          </div>
        )}
      </div>

      {info?.newer && (
        <section className="mt-4">
          <h4 className="update-heading">更新方法</h4>
          <p className="field-hint mb-2">在运行 cc-cluster 的主机上执行：</p>
          <Command text={info.command} />
          <p className="field-hint mt-2 leading-5">仅替换 cc-cluster 程序文件，槽位、节点、订阅与设置不变，运行中的容器不受影响。完成后刷新本页面。</p>
        </section>
      )}

      {info?.images_command && (
        <section className="mt-4">
          <h4 className="update-heading">镜像</h4>
          <p className="field-hint mb-2 leading-5">
            镜像设置仍为 <span className="mono">{info.images_from}</span>，可继续使用。更新至 <span className="mono">{info.current}</span> 后，运行中的槽位变为“配置待应用”，其持续检测暂停；重启网关并重建 worker 后生效（HOME 与身份保留）。
          </p>
          <Command text={info.images_command} />
        </section>
      )}

      {!!info?.releases.length && (
        <section className="mt-4">
          <h4 className="update-heading">更新说明</h4>
          <div className="grid gap-2.5">
            {info.releases.map((r) => (
              <article key={r.version} className="release">
                <header>
                  <strong className="mono">{r.version}</strong>
                  {publishedText(r) && <span>{publishedText(r)}</span>}
                  {safeHref(r.url) && (
                    <a href={safeHref(r.url)} target="_blank" rel="noopener noreferrer">
                      在 GitHub 查看
                      <ExternalLink size={12} />
                    </a>
                  )}
                </header>
                {r.notes ? <Markdown source={r.notes} /> : <p className="field-hint">未获取到更新说明，请在 GitHub 查看。</p>}
              </article>
            ))}
          </div>
        </section>
      )}

      {!info && !problem && checking && <p className="field-hint mt-3">加载中…</p>}
      {info && !info.auto_check && !latest && !problem && !checking && <p className="field-hint mt-3">自动检查已关闭。点击“检查更新”手动检查。</p>}
    </Dialog>
  );
}
