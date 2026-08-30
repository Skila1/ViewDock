export type PauseVerdict = "user" | "viewdock" | "unknown_avkit_or_webkit_or_hls";

/** Prefer an explicit user pause; otherwise a ViewDock video.pause() call; else not ours. */
export function classifyPauseSource(opts: {
  userPauseAgeMs: number | null;
  viewdockPauseAgeMs: number | null;
}): PauseVerdict {
  if (opts.userPauseAgeMs != null && opts.userPauseAgeMs <= 800) return "user";
  if (opts.viewdockPauseAgeMs != null && opts.viewdockPauseAgeMs <= 150) return "viewdock";
  return "unknown_avkit_or_webkit_or_hls";
}
