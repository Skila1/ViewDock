/**
 * The cut-off for "delete logs up to and including this day": the start of
 * the next day in the viewer's time zone, as an ISO time.
 */
export function pruneCutoff(day: string): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return null;
  const next = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + 1);
  return Number.isNaN(next.getTime()) ? null : next.toISOString();
}
