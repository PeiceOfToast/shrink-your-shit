// A settings row (the design's .srow for settings): the title and one muted description on the left, the controls on the
// right. Under 640 px the controls drop under the text (shell.css .setrow). ValueChip is the small pill that shows the
// current value (Main server, English); it reuses the .mchip class the lists already use.
import type { ReactNode } from "react";

export function SettingRow({ title, desc, children }: { title: ReactNode; desc?: ReactNode; children?: ReactNode }) {
  return (
    <div className="setrow">
      <div className="txt"><div className="ttl">{title}</div>{desc && <p className="muted sub">{desc}</p>}</div>
      {children && <div className="ctl">{children}</div>}
    </div>);
}

export function ValueChip({ children }: { children: ReactNode }) {
  return <span className="mchip">{children}</span>;
}
