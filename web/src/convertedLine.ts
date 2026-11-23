// Files converted (Dashboard): "70 per day", the average completed per day over the recorded days of the overview's daily
// series. Null until two days are recorded (the design's empty line then).
export interface DayLike { day: string; recorded: boolean; completed: number }

export function convertedLine(daily: DayLike[]): number | null {
  const rec = daily.filter((d) => d.recorded);
  if (rec.length < 2) return null;
  return Math.round(rec.reduce((a, d) => a + d.completed, 0) / rec.length);
}
