// “更多”菜单：触发按钮加一个 popover。弹出层在顶层显示，不受卡片与表格的裁剪；
// 点击别处、按 Esc 或选中一项后关闭，关闭后焦点回到触发按钮。
import { MoreHorizontal } from "lucide-react";
import type React from "react";
import { useEffect, useId, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { Button } from "./ui/button";

export interface MenuEntry {
  label: string;
  icon?: React.ComponentType<{ className?: string }>;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
}

const has = (name: string) => typeof HTMLElement !== "undefined" && name in HTMLElement.prototype;
/** 不支持 popover 的浏览器退回到跟在按钮下面的普通浮层。 */
const nativePopover = has("popover");
/** 支持 focusgroup 时方向键与菜单角色由浏览器提供；否则由下面的按键处理与显式角色补上。 */
const nativeFocusgroup = has("focusGroup");
/** 支持锚点定位时位置由样式表决定；否则打开前按触发按钮的位置计算。 */
const nativeAnchor = typeof CSS !== "undefined" && CSS.supports("position-area: block-end");

export function Menu({ items, disabled, label = "更多" }: { items: (MenuEntry | "sep")[]; disabled?: boolean; label?: string }) {
  const id = `menu-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  /** 这一次是用键盘打开的：打开后焦点进入第一项。 */
  const byKey = useRef(false);

  const enabled = () => Array.from(pop.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
  const hide = () => {
    if (nativePopover) pop.current?.hidePopover();
    else setOpen(false);
  };

  // 没有锚点定位时：右边缘与按钮对齐，下方放不下则放到上方。高度按项数估算，此时弹出层尚未显示，量不到。
  const place = () => {
    const el = pop.current;
    const r = button.current?.getBoundingClientRect();
    if (nativeAnchor || !nativePopover || !el || !r) return;
    const height = items.reduce((h, item) => h + (item === "sep" ? 9 : 36), 14);
    const above = r.bottom + 6 + height > window.innerHeight && r.top - 6 - height >= 0;
    el.style.top = `${above ? r.top - 6 - height : r.bottom + 6}px`;
    el.style.right = `${Math.max(8, window.innerWidth - r.right)}px`;
  };

  useEffect(() => {
    if (!open) return;
    // 没有锚点定位时弹出层不跟随滚动，滚动即关闭。
    const onScroll = () => !nativeAnchor && hide();
    const onDown = (e: MouseEvent) => !nativePopover && !wrap.current?.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => !nativePopover && e.key === "Escape" && (setOpen(false), button.current?.focus());
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const opened = () => {
    if (byKey.current) enabled()[0]?.focus();
    byKey.current = false;
  };
  useEffect(() => {
    if (!nativePopover && open) opened();
  }, [open]);

  const onMenuKey = (e: React.KeyboardEvent) => {
    const list = enabled();
    const at = list.indexOf(document.activeElement as HTMLButtonElement);
    const arrows = !nativeFocusgroup && (e.key === "ArrowDown" || e.key === "ArrowUp");
    if (!arrows && e.key !== "Home" && e.key !== "End") return;
    e.preventDefault();
    const next = e.key === "Home" ? 0 : e.key === "End" ? list.length - 1 : (at + (e.key === "ArrowUp" ? -1 : 1) + list.length) % list.length;
    list[next]?.focus({ preventScroll: true });
  };

  return (
    <div ref={wrap} data-menu className="relative" onClick={(e) => e.stopPropagation()}>
      <Button
        ref={button}
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        {...(nativePopover ? { popoverTarget: id } : { onClick: () => setOpen((v) => !v) })}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") byKey.current = !open;
          if (e.key !== "ArrowDown") return;
          e.preventDefault();
          if (open) return void enabled()[0]?.focus();
          byKey.current = true;
          if (nativePopover) pop.current?.showPopover();
          else setOpen(true);
        }}
      >
        <MoreHorizontal />
      </Button>
      {(nativePopover || open) && (
        <div
          ref={pop}
          id={id}
          className={cn("menu-pop", !nativePopover && "legacy")}
          {...(nativePopover ? { popover: "auto" as const } : {})}
          onBeforeToggle={(e) => e.newState === "open" && place()}
          onToggle={(e) => {
            setOpen(e.newState === "open");
            if (e.newState === "open") opened();
          }}
        >
          {/* focusgroup 放在里层：直接写在 popover 上时 Chromium 不提供方向键。 */}
          <div {...{ focusgroup: "menu nomemory" }} role={nativeFocusgroup ? undefined : "menu"} aria-label={label} onKeyDown={onMenuKey}>
            {items.map((item, i) =>
              item === "sep" ? (
                <hr key={i} className="menu-sep" />
              ) : (
                <button
                  key={item.label}
                  type="button"
                  role={nativeFocusgroup ? undefined : "menuitem"}
                  disabled={item.disabled}
                  onPointerMove={(e) => e.currentTarget.focus({ preventScroll: true })}
                  onClick={() => {
                    hide();
                    item.onSelect();
                  }}
                  className={cn("menu-item", item.danger && "danger")}
                >
                  {item.icon && <item.icon className="h-4 w-4 shrink-0 opacity-70" />}
                  {item.label}
                </button>
              ),
            )}
          </div>
        </div>
      )}
    </div>
  );
}
