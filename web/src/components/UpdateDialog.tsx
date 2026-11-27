// 版本与更新：当前版本、最新版本、发布时间，下面是发布页上写的更新说明。
// 页面只负责告诉使用者有新版本、该执行哪条命令；更新本身在终端里做。
import { ArrowUpCircle, CheckCircle2, Copy, ExternalLink, Info, RefreshCw, TriangleAlert } from "lucide-react";
import { Markdown } from "@/components/Markdown";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/toast";
import type { Release, UpdateInfo } from "@/lib/api";
import { clockTime, relativeTime } from "@/lib/utils";

interface Props {
  open: boolean;
  onClose: () => void;
  /** 页面加载时的版本；info 还没读到时先显示它。 */
  version?: string;
  info: UpdateInfo | null;
  checking: boolean;
  /** 连接口都没问成（服务停了之类）时的说明。 */
  error?: string;
  onCheck: () => void;
}

/** 发布时间；只查到版本号时接口给的是零值，显示成没有。 */
function publishedText(r?: Release): string {
  const ms = r ? Date.parse(r.published_at) : NaN;
  return Number.isNaN(ms) || ms < Date.UTC(2000, 0, 1) ? "" : clockTime(r!.published_at);
}

function Fact({ k, v, strong }: { k: string; v?: string; strong?: boolean }) {
  return (
    <div className="kv">
      <span>{k}</span>
      <strong className={strong ? "mono text-[var(--primary-strong)]" : "mono"}>{v || "—"}</strong>
    </div>
  );
}

function Command({ text }: { text: string }) {
  const toast = useToast();
  const copy = () => navigator.clipboard.writeText(text).then(() => toast("命令已复制。"), () => toast("复制失败，请手动选中复制。", "error"));
  return (
    <div className="flex items-stretch gap-2">
      <code className="code-line min-w-0 flex-1">{text}</code>
      <Button variant="outline" size="sm" onClick={copy} aria-label={`复制命令 ${text}`}>
        <Copy />
        复制
      </Button>
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
            {info?.checked_at ? `上次检查：${relativeTime(info.checked_at)}` : info ? "还没有检查过" : ""}
          </span>
          <Button variant="outline" onClick={onCheck} disabled={checking}>
            <RefreshCw className={checking ? "animate-spin" : undefined} />
            {checking ? "检查中…" : info?.checked_at ? "重新检查" : "检查更新"}
          </Button>
          <Button onClick={onClose}>关闭</Button>
        </>
      }
    >
      <div className="grid gap-2.5 sm:grid-cols-3">
        <Fact k="当前版本" v={current} />
        <Fact k="最新版本" v={latest?.version} strong={info?.newer} />
        <Fact k="发布时间" v={publishedText(latest)} />
      </div>

      <div className="mt-3 grid gap-2.5">
        {info?.newer && latest && (
          <div className="callout callout-info">
            <ArrowUpCircle size={16} />
            <span>
              有新版本 <span className="mono">{latest.version}</span>
              {info.releases.length > 1 && `，比现在的新 ${info.releases.length} 个版本`}。
            </span>
          </div>
        )}
        {info && !info.newer && latest && !fromSource && (
          <div className="callout callout-success">
            <CheckCircle2 size={16} />
            <span>已经是最新的版本。</span>
          </div>
        )}
        {fromSource && (
          <div className="callout callout-info">
            <Info size={16} />
            <span>这个 cc-cluster 是从源码构建的，不与发布的版本比较，也不自动更新：在源码目录里更新后重新构建。</span>
          </div>
        )}
        {problem && (
          <div className="callout callout-warning">
            <TriangleAlert size={16} />
            <span className="min-w-0 break-words">
              这次没有查成：{problem}
              {latest && "。下面是上次查到的结果。"}
            </span>
          </div>
        )}
      </div>

      {info?.newer && (
        <section className="mt-4">
          <h4 className="update-heading">怎么更新</h4>
          <p className="field-hint mb-2">在运行 cc-cluster 的那台机器的终端里执行：</p>
          <Command text={info.command} />
          <p className="field-hint mt-2 leading-5">
            它只换 cc-cluster 这个程序本身（下载后先核对校验和，失败就什么都不改）：槽位、节点、订阅和设置都不动，运行中的容器不受影响。
            换好后网页界面会自动重启，回到这里刷新页面就是新版本。
          </p>
        </section>
      )}

      {info?.images_command && (
        <section className="mt-4">
          <h4 className="update-heading">镜像</h4>
          <p className="field-hint mb-2 leading-5">
            镜像的设置仍是 <span className="mono">{info.images_from}</span> 的，照常使用不受影响。想换成 <span className="mono">{info.current}</span> 的，挑一个槽位里没在干活的时候执行下面的命令；
            之后把网关停止再启动、给槽位“重建 worker”才用上新镜像（HOME 和身份都保留）。
          </p>
          <Command text={info.images_command} />
        </section>
      )}

      {!!info?.releases.length && (
        <section className="mt-4">
          <h4 className="update-heading">{info.newer ? "更新内容" : "这个版本的更新内容"}</h4>
          <div className="grid gap-2.5">
            {info.releases.map((r) => (
              <article key={r.version} className="release">
                <header>
                  <strong className="mono">{r.version}</strong>
                  {publishedText(r) && <span>{publishedText(r)}</span>}
                  <a href={r.url} target="_blank" rel="noopener noreferrer">
                    在 GitHub 上看
                    <ExternalLink size={12} />
                  </a>
                </header>
                {r.notes ? <Markdown source={r.notes} /> : <p className="field-hint">没有取到这个版本的更新说明，可以到发布页上看。</p>}
              </article>
            ))}
          </div>
        </section>
      )}

      {!info && !problem && <p className="field-hint mt-3">{checking ? "正在读取…" : ""}</p>}
      {info && !latest && !problem && !checking && <p className="field-hint mt-3">没有开启自动检查。点“检查更新”去看一眼有没有新版本。</p>}
    </Dialog>
  );
}
