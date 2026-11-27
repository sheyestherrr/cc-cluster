// 外壳：侧栏加内容区，结构与样式取自 Resin（MIT）webui 的 components/AppShell.tsx，导航项换成本项目的。
import { Boxes, LayoutDashboard, Network, Rss, Settings } from "lucide-react";
import type React from "react";
import { cn } from "@/lib/utils";

export type PageId = "overview" | "slots" | "network" | "subs" | "system";

type Icon = React.ComponentType<{ size?: number }>;
interface NavItem { id: PageId; label: string; icon: Icon }

const navItems: NavItem[] = [
  { id: "overview", label: "总览看板", icon: LayoutDashboard },
  { id: "slots", label: "槽位", icon: Boxes },
  { id: "network", label: "节点与网关", icon: Network },
  { id: "subs", label: "订阅管理", icon: Rss },
  { id: "system", label: "系统", icon: Settings },
];
export const pageTitles: Record<PageId, string> = { overview: "总览", slots: "槽位", network: "节点与网关", subs: "订阅", system: "系统" };

/** 和标签页图标（favicon.svg）同一个图形：两个容器，上面一个出口。 */
function Logo() {
  return (
    <svg viewBox="0 0 32 32" aria-hidden width={22} height={22}>
      <rect fill="#1470ff" x="11.5" y="5" width="9" height="9" rx="2.5" />
      <rect fill="#1f2735" x="5" y="17" width="9" height="9" rx="2.5" />
      <rect fill="#1f2735" x="18" y="17" width="9" height="9" rx="2.5" />
    </svg>
  );
}

interface AppShellProps {
  activePage: PageId;
  onPageChange: (page: PageId) => void;
  cluster?: string;
  /** 正在运行的 cc-cluster 的版本；点它打开“版本与更新”。 */
  version?: string;
  /** 有新版本时是它的版本号。 */
  newVersion?: string;
  /** 页面打开之后 cc-cluster 被更新过：这个页面还是旧的，点一下刷新。 */
  stale?: boolean;
  onVersionClick?: () => void;
  /** 侧栏底部的一句现状。 */
  status?: { ready: number; total: number } | null;
  children: React.ReactNode;
}

function VersionChip({ version, newVersion, stale, onClick }: Pick<AppShellProps, "version" | "newVersion" | "stale"> & { onClick?: () => void }) {
  if (!version) return null;
  if (stale) {
    return (
      <button type="button" className="version-chip has-update" onClick={() => window.location.reload()} title="cc-cluster 已经更新，这个页面还是旧的">
        <span className="version-dot" />
        <span>已更新到 {version}，点这里刷新</span>
      </button>
    );
  }
  return (
    <button type="button" className={cn("version-chip", newVersion && "has-update")} onClick={onClick} title="查看版本与更新说明">
      <span className="mono version-number">{version}</span>
      {newVersion && (
        <>
          <span className="version-dot" />
          <span className="version-new">可更新到 {newVersion}</span>
        </>
      )}
    </button>
  );
}

export function AppShell({ activePage, onPageChange, cluster, version, newVersion, stale, onVersionClick, status, children }: AppShellProps) {
  return (
    <div className="app-layout">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-logo" aria-hidden="true">
            <Logo />
          </div>
          <div className="brand-copy">
            <div className="brand-title-row">
              <p className="brand-title">cc-cluster</p>
              {cluster && cluster !== "cc-cluster" && <span className="brand-version" title={cluster}>{cluster}</span>}
            </div>
            <VersionChip version={version} newVersion={newVersion} stale={stale} onClick={onVersionClick} />
          </div>
        </div>

        <div className="sidebar-main">
          <nav className="nav-list" aria-label="主导航">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activePage === item.id;
              return (
                <button key={item.id} type="button" aria-current={active ? "page" : undefined} className={cn("nav-item", active && "nav-item-active")} onClick={() => onPageChange(item.id)}>
                  <Icon size={16} />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
        </div>

        <div className="sidebar-bottom">
          <div className="sidebar-status">
            <span className={cn("status-dot", status?.ready && "ok")} />
            <span>{status ? `${status.ready} 个已接线 · 共 ${status.total} 个槽位` : "读取中…"}</span>
          </div>
        </div>
      </aside>

      <main className="main">
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
