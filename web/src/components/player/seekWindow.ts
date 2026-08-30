import { noteCurrentTimeWrite } from "@/playback/attachTrace";

/** True if the movie timestamp can be seeked inside the current HLS/MSE window. */
export function canSeekInWindow(opts: {
  targetMs: number;
  originMs: number;
  seekableStartSec?: number;
  seekableEndSec?: number;
  /** EVENT/live playlists on iOS report a sliding seekable start near the frontier. */
  ignoreSeekableStart?: boolean;
}): boolean {
  const { targetMs, originMs, seekableStartSec, seekableEndSec, ignoreSeekableStart } = opts;
  if (targetMs < originMs - 500) return false;
  if (seekableStartSec == null || seekableEndSec == null || !Number.isFinite(seekableStartSec) || !Number.isFinite(seekableEndSec)) {
    return Math.abs(targetMs - originMs) <= 2000;
  }
  const start = ignoreSeekableStart ? originMs : originMs + seekableStartSec * 1000;
  const end = originMs + seekableEndSec * 1000;
  return targetMs >= start - 500 && targetMs <= end + 2000;
}

/** If Safari jumped an EVENT playlist to the live edge, the playhead to restore. */
export function pinNativeLiveEdge(opts: {
  relSec: number;
  seekableEndSec: number;
  attachedAgoMs: number;
  lastStableRelSec: number;
}): number | null {
  const { relSec, seekableEndSec, attachedAgoMs, lastStableRelSec } = opts;
  if (!Number.isFinite(relSec) || !Number.isFinite(seekableEndSec) || seekableEndSec <= 0) {
    return null;
  }
  const nearLive = seekableEndSec - relSec < 5;
  if (!nearLive) return null;
  const justAttached = attachedAgoMs >= 0 && attachedAgoMs < 8000;
  const jumpedAhead = relSec - lastStableRelSec > 8;
  if (justAttached && relSec > 4) return 0;
  if (jumpedAhead) return Math.max(0, lastStableRelSec);
  return null;
}

export function holdNativeStart(
  video: HTMLVideoElement,
  attachedAt: number,
  lastStableMs: number,
  originMs: number,
): boolean {
  const end = video.seekable.length > 0 ? video.seekable.end(video.seekable.length - 1) : 0;
  const pin = pinNativeLiveEdge({
    relSec: video.currentTime || 0,
    seekableEndSec: Number.isFinite(end) ? end : 0,
    attachedAgoMs: attachedAt > 0 ? Date.now() - attachedAt : 0,
    lastStableRelSec: (lastStableMs - originMs) / 1000,
  });
  if (pin == null) return false;
  if (Math.abs((video.currentTime || 0) - pin) < 0.5) return false;
  noteCurrentTimeWrite(video, pin, "holdNativeStart.pin");
  video.currentTime = pin;
  return true;
}

export function seekableBounds(video: HTMLVideoElement): { startSec?: number; endSec?: number } {
  if (video.seekable.length === 0) return {};
  const startSec = video.seekable.start(0);
  const endSec = video.seekable.end(video.seekable.length - 1);
  if (!Number.isFinite(startSec) || !Number.isFinite(endSec)) return {};
  return { startSec, endSec };
}
