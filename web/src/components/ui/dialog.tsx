// 弹窗：原生 <dialog> 以模态方式打开，焦点约束、页面隔离、Esc 与焦点归还由浏览器处理；外观用 Resin（MIT）的 modal-card。
import { X } from "lucide-react";
import { useId, useLayoutEffect, useRef, type PointerEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Button } from "./button";

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  className?: string;
  /** 请求进行中：不能关闭，失败时才能在弹窗内显示原因。 */
  busy?: boolean;
  /** 带输入内容的表单：点击弹窗以外的区域不关闭，避免丢失已填写的内容。 */
  form?: boolean;
}

/** 浏览器是否支持 closedby 属性；不支持时点击弹窗以外关闭由下面的指针事件处理。 */
const nativeClosedBy = typeof HTMLDialogElement !== "undefined" && "closedBy" in HTMLDialogElement.prototype;

/** 打开后的初始焦点：表单是第一个输入框，确认类弹窗是页脚的第一个按钮（“取消”）。 */
const FOCUS_FIRST = ".modal-body input:not([type=checkbox]):not([type=radio]):not(:disabled), .modal-body textarea:not(:disabled), .modal-body select:not(:disabled), .modal-footer button:not(:disabled)";

export function Dialog({ open, onClose, title, description, children, footer, className, busy, form }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const close = useRef(onClose);
  close.current = onClose;
  const locked = useRef(busy);
  locked.current = busy;
  const pressedOutside = useRef(false);

  // 用布局副作用：卸载时节点还在文档里，close() 才能把焦点还给打开弹窗的控件。
  useLayoutEffect(() => {
    const el = ref.current;
    if (!open || !el) return;
    el.showModal();
    el.querySelector<HTMLElement>(FOCUS_FIRST)?.focus();
    return () => el.close();
  }, [open]);

  if (!open) return null;

  const outside = (e: PointerEvent<HTMLDialogElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };
  const light = !form && !busy;

  return (
    <dialog
      ref={ref}
      className={cn("card modal-card narrow", className)}
      aria-labelledby={titleId}
      {...{ closedby: busy ? "none" : form ? "closerequest" : "any" }}
      // Esc 与点击弹窗以外都先触发 cancel：由调用方的状态决定是否关闭。
      onCancel={(e) => {
        e.preventDefault();
        if (!locked.current) close.current();
      }}
      // 浏览器自行关闭了弹窗（连按两次 Esc 时 cancel 不可阻止）：请求进行中重新打开，否则同步状态。
      onClose={(e) => {
        if (locked.current) e.currentTarget.showModal();
        else close.current();
      }}
      onPointerDown={nativeClosedBy ? undefined : (e) => (pressedOutside.current = light && outside(e))}
      onPointerUp={nativeClosedBy ? undefined : (e) => pressedOutside.current && light && outside(e) && close.current()}
    >
      <div className="modal-header">
        <h1 id={titleId}>{title}</h1>
        <Button variant="ghost" size="icon" aria-label="关闭" disabled={busy} onClick={onClose}>
          <X />
        </Button>
      </div>
      {description && <div className="modal-desc">{description}</div>}
      {/* 请求进行中整个表单不可操作：页脚的“取消”与输入框都不应再改动。 */}
      {children && <div className="modal-body" inert={busy}>{children}</div>}
      {footer && <div className="modal-footer" inert={busy}>{footer}</div>}
    </dialog>
  );
}
