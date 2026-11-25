// 跟在触发按钮下面的小菜单：点外面、按 Esc 或选中一项后关闭。
import type React from "react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface MenuEntry {
  label: string;
  icon?: React.ComponentType<{ className?: string }>;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
}

export function Menu({ trigger, items }: { trigger: (toggle: () => void) => ReactNode; items: (MenuEntry | "sep")[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const down = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false);
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", down);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("mousedown", down);
      window.removeEventListener("keydown", key);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative" onClick={(e) => e.stopPropagation()}>
      {trigger(() => setOpen((v) => !v))}
      {open && (
        <div className="menu-pop" role="menu">
          {items.map((item, i) =>
            item === "sep" ? (
              <div key={i} className="menu-sep" />
            ) : (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                disabled={item.disabled}
                onClick={() => {
                  setOpen(false);
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
      )}
    </div>
  );
}
