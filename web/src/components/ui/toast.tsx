// 提示条：外观用 Resin（MIT）的 toast-container / toast-item，从页面上方落下。
// 只用于操作成功这类看过即可的结果；需要处理的事项留在页面上（见 components/Actions.tsx 的 Notice）。
import { AlertCircle, CheckCircle2, X } from "lucide-react";
import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from "react";

type Kind = "success" | "error";
interface Toast {
  id: number;
  kind: Kind;
  text: string;
}

const Ctx = createContext<(text: string, kind?: Kind) => void>(() => {});

export function useToast() {
  return useContext(Ctx);
}

/** 停留时间随字数增加：短句 3 秒，每多一个字加 80 毫秒，最长 10 秒；失败的提示至少 6 秒。 */
function lifetime(text: string, kind: Kind): number {
  return Math.min(10_000, Math.max(kind === "error" ? 6000 : 3000, 1500 + text.length * 80));
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    clearTimeout(timers.current.get(id));
    timers.current.delete(id);
    setItems((v) => v.filter((t) => t.id !== id));
  }, []);
  const arm = useCallback((t: Toast) => timers.current.set(t.id, setTimeout(() => dismiss(t.id), lifetime(t.text, t.kind))), [dismiss]);

  const push = useCallback(
    (text: string, kind: Kind = "success") => {
      const t = { id: Date.now() + Math.random(), kind, text };
      setItems((v) => [...v.slice(-3), t]);
      arm(t);
    },
    [arm],
  );

  return (
    <Ctx.Provider value={push}>
      {children}
      <div className="toast-container" role="region" aria-label="通知" aria-live="polite">
        {items.map((t) => (
          // 指针停在提示条上时不计时，移开后重新计时：较长的句子可以读完。
          <div key={t.id} className={`toast-item ${t.kind === "error" ? "toast-error" : "toast-success"}`} onPointerEnter={() => clearTimeout(timers.current.get(t.id))} onPointerLeave={() => arm(t)}>
            {t.kind === "error" ? <AlertCircle size={16} className="mt-0.5 shrink-0" /> : <CheckCircle2 size={16} className="mt-0.5 shrink-0" />}
            <span className="toast-text whitespace-pre-wrap break-words">{t.text}</span>
            <button type="button" className="toast-close" aria-label="关闭通知" onClick={() => dismiss(t.id)}>
              <X size={14} />
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}
