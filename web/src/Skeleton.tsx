// Grey blocks in the shape of a list page's loaded layout (C7, U9). It shows on a first load only, in the content area:
// the page header and tabs render for real around it. It reuses the real tile, row and card classes so the blocks take
// the same heights as the loaded rows. Static on purpose: no shimmer, so nothing moves under reduced motion either.
export function Skeleton({ tiles = 0, rows = 0, cards = 0 }: { tiles?: number; rows?: number; cards?: number }) {
  const n = (k: number) => Array.from({ length: k }, (_, i) => i);
  return (
    <div className="skel" aria-busy="true">
      <span role="status"><span className="sr">Loading</span></span>
      <div aria-hidden="true">
        {tiles > 0 && <section className="tiles">{n(tiles).map((i) => (
          <div className="tile" key={i}><span className="skel-b skel-w25" /><span className="skel-b skel-big" /><span className="skel-b skel-w60" /></div>
        ))}</section>}
        {rows > 0 && <div className="list rows"><table className="tbl"><tbody>{n(rows).map((i) => (
          <tr key={i}>
            <td className="tile"><span className="ltile skel-tile" /></td>
            <td className="idc"><span className="skel-b skel-w40" /><span className="skel-b skel-w60" /></td>
            <td className="stc"><span className="skel-b skel-pill" /></td>
          </tr>
        ))}</tbody></table></div>}
        {cards > 0 && <div className="wgrid">{n(cards).map((i) => (
          <article className="card wcard" key={i}>
            <div className="hd"><span className="skel-b skel-w40" /></div>
            <div className="bd stack"><span className="skel-b skel-w60" /><span className="skel-b skel-w25" /><span className="skel-b skel-w60" /></div>
          </article>
        ))}</div>}
      </div>
    </div>
  );
}
