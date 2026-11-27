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
/** 各页面的名称：导航、页头、标签页标题和正文中的引用都取自这里。 */
export const pageTitles = Object.fromEntries(navItems.map((item) => [item.id, item.label])) as Record<PageId, string>;

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
      <button type="button" className="version-chip is-stale" onClick={() => window.location.reload()} title="cc-cluster 已更新，刷新页面后生效">
        <span>已更新至 {version}，点击刷新</span>
      </button>
    );
  }
  const label = newVersion ? `当前版本 ${version}，有新版本 ${newVersion}` : `当前版本 ${version}`;
  return (
    <button type="button" className="version-chip" onClick={onClick} title={newVersion ? `有新版本 ${newVersion}` : "版本与更新"} aria-label={label}>
      <span className="mono version-number">{version}</span>
      {newVersion && <span className="version-dot" aria-hidden="true" />}
    </button>
  );
}

export function AppShell({ activePage, onPageChange, version, newVersion, stale, onVersionClick, status, children }: AppShellProps) {
  return (
    <div className="app-layout">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-logo" aria-hidden="true">
            <Logo />
          </div>
          <div className="brand-copy">
            <p className="brand-title">cc-cluster</p>
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
            <span>{status ? `${status.ready} 个已接线 · 共 ${status.total} 个槽位` : "加载中…"}</span>
          </div>
        </div>
      </aside>

      <main className="main">
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
