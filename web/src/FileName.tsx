import { Fragment } from "react";

// A file name may wrap only after . _ - (design: names wrap at those and stop at 2 lines with "..."). <wbr> rather
// than a zero-width space, so a copied name stays exact.
export const FileName = ({ n }: { n: string }) => <>{n.split(/(?<=[._-])/).map((p, i) => <Fragment key={i}>{i > 0 && <wbr />}{p}</Fragment>)}</>;
