// A file still changing: a scan row on the Waiting tab that joins the queue once it stops changing. The server sends it as
// state queued with subState settling and no job, so it has nothing to open, move or cancel.
export const SETTLING_WORD = "Still changing";
export const isSettling = (j: { state: string; subState?: string | null }) => j.state === "queued" && j.subState === "settling";
