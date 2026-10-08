/** Seconds to expose on MediaSource so AVKit/MSE report the movie, not the EVENT window. */
export function movieDurationSec(durationMs: number | null | undefined): number | null {
  if (durationMs == null || !(durationMs > 0) || !Number.isFinite(durationMs)) return null;
  return durationMs / 1000;
}

export function needsDurationPin(current: number, movieSec: number): boolean {
  if (!(movieSec > 0)) return false;
  if (!Number.isFinite(current)) return true;
  return current + 0.5 < movieSec;
}

type OpenMediaSource = {
  readyState?: string;
  duration: number;
  sourceBuffers?: { length: number; [i: number]: { updating?: boolean } };
};

/** Returns true when duration was written. Never throws. */
export function pinOpenMediaSource(ms: OpenMediaSource | null | undefined, movieSec: number): boolean {
  if (!ms || ms.readyState !== "open" || !(movieSec > 0)) return false;
  const bufs = ms.sourceBuffers;
  if (bufs) {
    for (let i = 0; i < bufs.length; i++) {
      if (bufs[i]?.updating) return false;
    }
  }
  if (!needsDurationPin(ms.duration, movieSec)) return false;
  try {
    ms.duration = movieSec;
    return true;
  } catch {
    return false;
  }
}
