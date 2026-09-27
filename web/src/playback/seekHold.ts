export type SeekHoldAction = "begin" | "keep" | "apply" | "clear" | "timeout" | "ignore";

/** Far past the generated edge: start a new FFmpeg session instead of waiting for remux from 0. */
export function shouldReplaceForGenerated(nowSec: number, playlistEdgeSec: number, slackSec = 8): boolean {
  return Number.isFinite(nowSec) && Number.isFinite(playlistEdgeSec) && nowSec > playlistEdgeSec + slackSec;
}

/** AVKit can seek to a movie time remux has not written yet. */
export function captureSeekHold(nowSec: number, playlistEdgeSec: number, movieSec: number | null): number | null {
  if (!Number.isFinite(nowSec) || nowSec <= 0) return null;
  if (nowSec <= playlistEdgeSec + 0.75) return null;
  if (movieSec != null && nowSec > movieSec + 0.5) return null;
  return nowSec;
}

/** EVENT remux is not live. A seek past the generated edge must wait, not snap to live-sync. */
export function seekHoldAction(opts: {
  requestedSec: number | null;
  nowSec: number;
  playlistEdgeSec: number;
  movieSec: number | null;
  endlist: boolean;
  heldForMs: number;
  maxHoldMs?: number;
}): SeekHoldAction {
  const hold = opts.requestedSec;
  if (hold == null || !(hold > 0)) return "ignore";
  if (opts.heldForMs > (opts.maxHoldMs ?? 60_000)) return "timeout";
  if (opts.movieSec != null && hold > opts.movieSec + 1) return "clear";
  if (opts.endlist && hold > opts.playlistEdgeSec + 1) return "clear";
  if (opts.playlistEdgeSec >= hold - 0.35) return "apply";
  if (opts.nowSec + 1.25 < hold) return "keep";
  return "ignore";
}
