// A settings row (the design's .srow for settings): the title and one muted description on the left, the controls on the
// right. Under 640 px the controls drop under the text (shell.css .setrow). ValueChip is the small pill that shows the
// current value (Main server, English); it reuses the .mchip class the lists already use.
import type { ReactNode } from "react";

// descId names the description, so a control can point aria-describedby at it.
// wide gives the controls the width they need (a row of four Accent choices), so they stay on one line.
export function SettingRow({ title, desc, descId, wide, children }: { title: ReactNode; desc?: ReactNode; descId?: string; wide?: boolean; children?: ReactNode }) {
  return (
    <div className={wide ? "setrow wide" : "setrow"}>
      <div className="txt"><div className="ttl">{title}</div>{desc && <p className="muted sub" id={descId}>{desc}</p>}</div>
      {children && <div className="ctl">{children}</div>}
    </div>);
}

export function ValueChip({ children }: { children: ReactNode }) {
  return <span className="mchip">{children}</span>;
}
