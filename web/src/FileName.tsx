import { Fragment } from "react";

// A file name may wrap only after . _ - (design: names wrap at those and stop at 2 lines with "..."), never right
// before the extension: the last part stays joined to the one before it ("Wedding." / "2019.mp4"). <wbr> rather
// than a zero-width space, so a copied name stays exact.
export const FileName = ({ n }: { n: string }) => {
  const p = n.split(/(?<=[._-])/);
  if (p.length > 1) p.splice(-2, 2, p[p.length - 2] + p[p.length - 1]);
  return <>{p.map((s, i) => <Fragment key={i}>{i > 0 && <wbr />}{s}</Fragment>)}</>;
};
