// 开关：外观取自 Resin（MIT）webui 的 components/ui/Switch.tsx 与 Switch.css，属性沿用本项目原有的。
interface SwitchProps {
  checked: boolean;
  onCheckedChange: (value: boolean) => void;
  disabled?: boolean;
  id?: string;
  label?: string;
}

export function Switch({ checked, onCheckedChange, disabled, id, label }: SwitchProps) {
  return (
    <label className="switch shrink-0" style={disabled ? { opacity: 0.5 } : undefined}>
      <input id={id} type="checkbox" role="switch" className="switch-input" aria-label={label} checked={checked} disabled={disabled} onChange={(e) => onCheckedChange(e.target.checked)} />
      <span className="switch-slider" />
    </label>
  );
}
