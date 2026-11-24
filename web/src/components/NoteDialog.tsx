// 备注的显示与编辑：槽位、网关、节点、订阅共用。本项目自己写的。
import { useEffect, useState } from "react";
import { ErrorBox } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NOTE_MAX, api, errorText, type NoteKind } from "@/lib/api";

export const NOTE_PLACEHOLDER = "可不填；中文、下划线、空格都可以，只用于显示";

/** 要改备注的对象：what 是种类的中文名，用在标题里。 */
export type NoteTarget = { kind: NoteKind; what: string; id: string; note?: string };

/** 跟在名称后面的备注；没有备注时什么也不显示。 */
export function NoteText({ note }: { note?: string }) {
  if (!note) return null;
  return <span title={note} className="min-w-0 max-w-[45%] shrink truncate text-[12.5px] text-muted">{note}</span>;
}

export function NoteDialog({ target, onClose, onSaved }: { target: NoteTarget | null; onClose: () => void; onSaved: (text: string) => void }) {
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    setNote(target?.note ?? "");
    setError("");
  }, [target]);

  const save = async () => {
    if (!target) return;
    setSaving(true);
    setError("");
    try {
      const text = note.trim();
      await api.setNote(target.kind, target.id, text);
      onSaved(text ? `${target.what} ${target.id} 的备注已更新` : `已清掉${target.what} ${target.id} 的备注`);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={!!target}
      onClose={onClose}
      title={`${target?.what ?? ""} ${target?.id ?? ""} 的备注`}
      description="名称要用在容器名和文件名里，只能是小写字母、数字和连字符，登记后不能改；想写中文、下划线或更长的说明，放在这里。备注只用于显示，随时可以改，不影响运行中的槽位。"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving}>{saving ? "保存中…" : "保存"}</Button>
        </>
      }
    >
      <div className="space-y-3">
        <Input
          aria-label="备注"
          value={note}
          maxLength={NOTE_MAX}
          autoFocus
          placeholder="留空表示不要备注"
          onChange={(e) => setNote(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing && !saving) void save();
          }}
        />
        {error && <ErrorBox text={error} />}
      </div>
    </Dialog>
  );
}
