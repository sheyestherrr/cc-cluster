// 操作的进行状态与结果：放在应用一层，切换页面后“启动中…”与失败原因仍然保留。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ErrorBox } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { useToast } from "@/components/ui/toast";
import { ApiError, errorText } from "@/lib/api";

/** 留在页面上、需要使用者关闭的一条说明：失败原因，或成功后仍需处理的事项。 */
export interface Notice {
  tone: "error" | "warning";
  text: string;
  log?: string[];
}

/** 执行前需要确认的操作：会中断正在运行的程序，或无法撤销。 */
export interface Confirm {
  title: string;
  text: ReactNode;
  /** 确认按钮上的文字。 */
  action: string;
  /** 不满足条件时确认按钮不可用，原因写在 text 中。 */
  blocked?: boolean;
  onConfirm: () => void;
}

interface Actions {
  /** 打开确认弹窗；确认后执行 onConfirm。 */
  confirm: (c: Confirm) => void;
  /** 正在进行的操作：键是“种类:名称”，值是按钮上显示的文字。 */
  busy: Record<string, string>;
  notice: Notice | null;
  setNotice: (n: Notice | null) => void;
  /**
   * 执行一次操作。结束后先重新读取状态再解除“进行中”，按钮不会短暂退回旧状态。
   * 成功时以提示条显示 done，返回 true；失败时把原因留在页面上，返回 false。
   */
  run: <T>(key: string, label: string, action: () => Promise<T>, done?: string | ((r: T) => string)) => Promise<boolean>;
}

const Ctx = createContext<Actions | null>(null);

export function useActions(): Actions {
  const v = useContext(Ctx);
  if (!v) throw new Error("useActions 只能在 ActionsProvider 内使用");
  return v;
}

export function ActionsProvider({ refresh, children }: { refresh: () => Promise<void>; children: ReactNode }) {
  const toast = useToast();
  const [busy, setBusy] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<Notice | null>(null);

  const run = useCallback<Actions["run"]>(
    async (key, label, action, done) => {
      setBusy((b) => ({ ...b, [key]: label }));
      setNotice(null);
      let ok = true;
      let text = "";
      try {
        const r = await action();
        if (done) text = typeof done === "string" ? done : done(r);
      } catch (e) {
        ok = false;
        setNotice({ tone: "error", text: errorText(e), log: e instanceof ApiError ? e.log : [] });
      }
      await refresh();
      setBusy((b) => {
        const { [key]: _, ...rest } = b;
        return rest;
      });
      if (text) toast(text);
      return ok;
    },
    [refresh, toast],
  );

  const [confirming, confirm] = useState<Confirm | null>(null);
  const value = useMemo(() => ({ busy, notice, setNotice, run, confirm }), [busy, notice, run]);
  return (
    <Ctx.Provider value={value}>
      {children}
      <Dialog
        open={!!confirming}
        onClose={() => confirm(null)}
        title={confirming?.title ?? ""}
        description={confirming?.text}
        footer={
          <>
            <Button variant="outline" onClick={() => confirm(null)}>取消</Button>
            <Button
              variant="destructive"
              disabled={confirming?.blocked}
              onClick={() => {
                confirm(null);
                confirming?.onConfirm();
              }}
            >
              {confirming?.action}
            </Button>
          </>
        }
      />
    </Ctx.Provider>
  );
}

/** 当前的说明；出现时滚动到可见位置，从页面下方触发的操作也能看到结果。 */
export function ActionNotice() {
  const { notice, setNotice } = useActions();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [notice]);
  if (!notice) return null;
  return (
    <div ref={ref} className="scroll-mt-4">
      <ErrorBox tone={notice.tone} text={notice.text} log={notice.log} onClose={() => setNotice(null)} />
    </div>
  );
}
