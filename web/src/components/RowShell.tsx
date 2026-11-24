// 列表里的一行：行首图标（知道地区时是国旗）、标题与标签、一行说明、右侧的操作和菜单。
// 原先写在 NetworkPage.tsx 里，订阅页也要用，所以单独放出来。
import { Loader2, MoreHorizontal } from "lucide-react";
import type React from "react";
import { RegionIcon } from "@/components/Flag";
import { Menu, type MenuEntry } from "@/components/Menu";
import { NoteText } from "@/components/NoteDialog";
import { RowTitle, selectableRow } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";

export interface Selectable {
  selected: boolean;
  onSelect: () => void;
}

export function RowShell({ icon, region, regionFrom, title, note, tags, detail, side, busy, action, menu, selected, onSelect }: Selectable & {
  icon: React.ComponentType<{ className?: string }>;
  /** 知道地区时行首显示国旗，不知道时显示 icon。 */
  region?: string;
  regionFrom?: string;
  title: string;
  note?: string;
  tags?: React.ReactNode;
  detail: React.ReactNode;
  side?: React.ReactNode;
  busy?: string;
  action?: React.ReactNode;
  menu: (MenuEntry | "sep")[];
}) {
  return (
    <div {...selectableRow(selected, onSelect)}>
      <RegionIcon code={region} from={regionFrom} icon={icon} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <RowTitle selected={selected} onSelect={onSelect}>{title}</RowTitle>
          <NoteText note={note} />
          {tags}
        </div>
        <div className="selectable truncate text-[11.5px] text-muted">{detail}</div>
      </div>
      {side && <span className="hidden max-w-[160px] shrink-0 truncate text-[11.5px] text-muted min-[1500px]:inline">{side}</span>}
      {busy ? (
        <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-muted">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          {busy}
        </span>
      ) : action}
      <Menu
        trigger={(toggle) => (
          <Button variant="ghost" size="iconSm" aria-label="更多" onClick={toggle} disabled={!!busy}>
            <MoreHorizontal />
          </Button>
        )}
        items={menu}
      />
    </div>
  );
}
