// 改编自 Claude-Hydra（MIT）app/src/components/AppShell.tsx，v1.1.6：
// 去掉窗口控制、更新提示和应用图标，导航项换成本项目的。
import { Boxes, LayoutDashboard, Moon, Network, Rss, Settings, Sun, SunMoon } from "lucide-react";
import type React from "react";
import { useLayoutEffect, useRef, useState } from "react";
import type { ThemeMode } from "@/lib/theme";
import { cn } from "@/lib/utils";

export type PageId = "overview" | "slots" | "network" | "subs" | "system";

type Icon = React.ComponentType<{ className?: string }>;
interface NavItem { id: PageId; label: string; icon: Icon }

const main: NavItem[] = [
  { id: "overview", label: "总览", icon: LayoutDashboard },
  { id: "slots", label: "槽位", icon: Boxes },
  { id: "network", label: "节点与网关", icon: Network },
  { id: "subs", label: "订阅", icon: Rss },
];
const footer: NavItem[] = [{ id: "system", label: "系统", icon: Settings }];
export const pageTitles: Record<PageId, string> = { overview: "总览", slots: "槽位", network: "节点与网关", subs: "订阅", system: "系统" };

/** 和标签页图标（favicon.svg）同一个图形：两个容器，上面一个出口。 */
function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={className}>
      <rect className="fill-fg" width="32" height="32" rx="7" />
      <rect className="fill-[#a5b4fc] dark:fill-[#4f46e5]" x="11.5" y="6" width="9" height="9" rx="2.5" />
      <rect className="fill-card" x="6" y="17" width="9" height="9" rx="2.5" />
      <rect className="fill-card" x="17" y="17" width="9" height="9" rx="2.5" />
    </svg>
  );
}

const nextTheme: Record<ThemeMode, ThemeMode> = { system: "light", light: "dark", dark: "system" };
const themeLabel: Record<ThemeMode, string> = { system: "跟随系统", light: "浅色", dark: "深色" };

interface AppShellProps {
  activePage: PageId;
  onPageChange: (page: PageId) => void;
  theme: ThemeMode;
  onThemeChange: (mode: ThemeMode) => void;
  /** Running summary shown at the bottom of the sidebar. */
  status?: { ready: number; total: number } | null;
  children: React.ReactNode;
}

function NavRow({ item, active, onSelect, setRef }: { item: NavItem; active: boolean; onSelect: () => void; setRef: (el: HTMLButtonElement | null) => void }) {
  const Icon = item.icon;
  return (
    <button
      ref={setRef}
      onClick={onSelect}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative z-10 flex h-[30px] w-full items-center gap-[9px] rounded-[7px] px-2 text-left text-[13px] active:!scale-100",
        active ? "font-medium text-fg" : "text-fg-2 hover:bg-pill hover:text-fg",
      )}
    >
      <Icon className={cn("h-[15px] w-[15px] shrink-0", active ? "text-fg" : "text-muted")} />
      {item.label}
    </button>
  );
}

export function AppShell({ activePage, onPageChange, theme, onThemeChange, status, children }: AppShellProps) {
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : SunMoon;
  const refs = useRef<Partial<Record<PageId, HTMLButtonElement | null>>>({});
  const nav = useRef<HTMLDivElement>(null);
  const [thumb, setThumb] = useState<number | null>(null);

  // Sliding highlight behind the active row.
  useLayoutEffect(() => {
    const measure = () => {
      const el = refs.current[activePage];
      const box = nav.current;
      if (el && box) setThumb(el.getBoundingClientRect().top - box.getBoundingClientRect().top);
    };
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [activePage]);

  const row = (item: NavItem) => (
    <NavRow key={item.id} item={item} active={activePage === item.id} onSelect={() => onPageChange(item.id)} setRef={(el) => (refs.current[item.id] = el)} />
  );

  return (
    <div className="flex h-screen overflow-hidden bg-bg text-fg">
      <aside className="flex w-[204px] shrink-0 flex-col border-r-[0.5px] border-line bg-bg">
        <div className="flex h-12 shrink-0 items-center gap-2 pl-4">
          <Logo className="h-[18px] w-[18px] shrink-0" />
          <span className="truncate text-[13.5px] font-semibold tracking-[0.01em]">cc-cluster</span>
        </div>

        <div ref={nav} className="relative flex min-h-0 flex-1 flex-col px-2.5">
          {thumb !== null && (
            <span
              aria-hidden
              className="absolute inset-x-2.5 h-[30px] rounded-[7px] bg-card shadow-edge transition-[top] duration-300 ease-soft dark:bg-white/[.07]"
              style={{ top: thumb }}
            />
          )}
          <div className="flex-1 space-y-0.5 overflow-y-auto pb-2 pt-1">{main.map(row)}</div>
          <div className="space-y-0.5 border-t-[0.5px] border-line-2 py-2">{footer.map(row)}</div>
        </div>

        <div className="flex h-9 shrink-0 items-center gap-2 px-[18px] pb-1 text-[11.5px] text-muted">
          <span className={cn("h-[7px] w-[7px] rounded-full", status?.ready ? "bg-ok shadow-[0_0_0_3px_rgb(var(--ok-soft))]" : "bg-faint")} />
          {status ? `${status.ready} 个已接线 · 共 ${status.total} 个槽位` : "读取中…"}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center justify-between gap-3 pl-5 pr-3">
          <div className="min-w-0 truncate text-[13.5px] font-semibold">{pageTitles[activePage]}</div>
          <button
            onClick={() => onThemeChange(nextTheme[theme])}
            title={`主题：${themeLabel[theme]}（点击切换）`}
            aria-label={`主题：${themeLabel[theme]}`}
            className="flex h-[26px] w-[26px] items-center justify-center rounded-[7px] text-muted hover:bg-pill hover:text-fg"
          >
            <ThemeIcon className="h-[15px] w-[15px]" />
          </button>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">{children}</main>
      </div>
    </div>
  );
}
