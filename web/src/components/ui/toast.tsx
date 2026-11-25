// 提示条：外观用 Resin（MIT）的 toast-container / toast-item，从页面上方落下。
import { AlertCircle, CheckCircle2 } from "lucide-react";
import { createContext, useCallback, useContext, useState, type ReactNode } from "react";

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

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const push = useCallback((text: string, kind: Kind = "success") => {
    const id = Date.now() + Math.random();
    setItems((v) => [...v.slice(-3), { id, kind, text }]);
    setTimeout(() => setItems((v) => v.filter((t) => t.id !== id)), kind === "error" ? 6000 : 2600);
  }, []);
  return (
    <Ctx.Provider value={push}>
      {children}
      <div className="toast-container" aria-live="polite">
        {items.map((t) => (
          <div key={t.id} className={`toast-item ${t.kind === "error" ? "toast-error" : "toast-success"}`}>
            {t.kind === "error" ? <AlertCircle size={16} className="mt-0.5 shrink-0" /> : <CheckCircle2 size={16} className="mt-0.5 shrink-0" />}
            <span className="toast-text whitespace-pre-wrap break-words">{t.text}</span>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}
