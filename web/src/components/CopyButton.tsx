// 复制按钮：结果直接显示在按钮上。提示条在弹窗打开时被遮罩挡住，所以不用它。
import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { Button, type ButtonProps } from "./ui/button";

export function CopyButton({ text, label = "复制", ...props }: { text: string; label?: string } & Omit<ButtonProps, "onClick" | "children">) {
  const [result, setResult] = useState<"" | "ok" | "failed">("");
  useEffect(() => {
    if (!result) return;
    const timer = setTimeout(() => setResult(""), 2000);
    return () => clearTimeout(timer);
  }, [result]);

  // 非安全上下文中没有 navigator.clipboard，此时同样显示失败。
  const copy = () => {
    const done = navigator.clipboard?.writeText(text);
    if (!done) return setResult("failed");
    done.then(() => setResult("ok"), () => setResult("failed"));
  };

  return (
    <Button {...props} onClick={copy}>
      {result === "ok" ? <Check /> : <Copy />}
      <span aria-live="polite">{result === "ok" ? "已复制" : result === "failed" ? "复制失败，请手动复制" : label}</span>
    </Button>
  );
}
