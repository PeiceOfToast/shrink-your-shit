// A settings row (the design's .srow for settings): the title and one muted description on the left, the controls on the
// right. Under 640 px the controls drop under the text (shell.css .setrow). ValueChip is the small pill that shows the
// current value (Main server, English); it reuses the .mchip class the lists already use.
import type { ReactNode } from "react";

// descId names the description, so a control can point aria-describedby at it.
export function SettingRow({ title, desc, descId, children }: { title: ReactNode; desc?: ReactNode; descId?: string; children?: ReactNode }) {
  return (
    <div className="setrow">
      <div className="txt"><div className="ttl">{title}</div>{desc && <p className="muted sub" id={descId}>{desc}</p>}</div>
      {children && <div className="ctl">{children}</div>}
    </div>);
}

export function ValueChip({ children }: { children: ReactNode }) {
  return <span className="mchip">{children}</span>;
}
