// Files converted (Dashboard): "70 per day · today 5 done, 1 skipped", from the overview's daily series. Null until two
// days are recorded (the design's empty line then). perDay is the average over the recorded days; today is the last day
// of the series (the server's days run oldest first, ending today), or null when that day isn't recorded.
export interface DayLike { day: string; recorded: boolean; completed: number; skipped: number }

export function convertedLine(daily: DayLike[]): { perDay: number; today: { done: number; skipped: number } | null } | null {
  const rec = daily.filter((d) => d.recorded);
  if (rec.length < 2) return null;
  const last = daily[daily.length - 1];
  return { perDay: Math.round(rec.reduce((a, d) => a + d.completed, 0) / rec.length), today: last?.recorded ? { done: last.completed, skipped: last.skipped } : null };
}
