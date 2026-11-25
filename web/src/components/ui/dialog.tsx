// 弹窗：外观用 Resin（MIT）的 modal-overlay / modal-card；按 Esc 只关最上面一层的做法沿用原先的实现。
import { X } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Button } from "./button";
import { Card } from "./card";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  className?: string;
}

/** 打开着的弹窗，最上面的排在最后；Esc 只关最上面那个。 */
const stack: string[] = [];

export function Dialog({ open, onClose, title, description, children, footer, className }: DialogProps) {
  const key = useId();
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    if (!open) return;
    stack.push(key);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && stack[stack.length - 1] === key) {
        e.stopImmediatePropagation();
        close.current();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      const i = stack.indexOf(key);
      if (i >= 0) stack.splice(i, 1);
    };
  }, [open, key]);

  if (!open) return null;
  return (
    <div data-dialog-layer className="modal-overlay" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <Card className={cn("modal-card narrow", className)}>
        <div className="modal-header">
          <h3>{title}</h3>
          <Button variant="ghost" size="icon" aria-label="关闭" onClick={onClose}>
            <X />
          </Button>
        </div>
        {description && <div className="modal-desc">{description}</div>}
        {children && <div className="modal-body">{children}</div>}
        {footer && <div className="modal-footer">{footer}</div>}
      </Card>
    </div>
  );
}
