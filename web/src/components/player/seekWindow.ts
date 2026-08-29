/** True if the movie timestamp can be seeked inside the current HLS/MSE window. */
export function canSeekInWindow(opts: {
  targetMs: number;
  originMs: number;
  seekableStartSec?: number;
  seekableEndSec?: number;
}): boolean {
  const { targetMs, originMs, seekableStartSec, seekableEndSec } = opts;
  if (targetMs < originMs - 500) return false;
  if (seekableStartSec == null || seekableEndSec == null || !Number.isFinite(seekableStartSec) || !Number.isFinite(seekableEndSec)) {
    return Math.abs(targetMs - originMs) <= 2000;
  }
  const start = originMs + seekableStartSec * 1000;
  const end = originMs + seekableEndSec * 1000;
  return targetMs >= start - 500 && targetMs <= end + 2000;
}

export function seekableBounds(video: HTMLVideoElement): { startSec?: number; endSec?: number } {
  if (video.seekable.length === 0) return {};
  const startSec = video.seekable.start(0);
  const endSec = video.seekable.end(video.seekable.length - 1);
  if (!Number.isFinite(startSec) || !Number.isFinite(endSec)) return {};
  return { startSec, endSec };
}
