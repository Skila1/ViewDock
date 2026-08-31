import { noteCurrentTimeWrite } from "@/playback/attachTrace";

/** Seconds of media actually generated/buffered. Not the pinned movie duration. */
export function generatedMediaEndSec(video: HTMLVideoElement): number | undefined {
  if (video.buffered.length === 0) return undefined;
  const end = video.buffered.end(video.buffered.length - 1);
  return Number.isFinite(end) ? end : undefined;
}

/**
 * Native EVENT HLS: Safari's video.duration / seekable end is often the movie
 * (or Infinity). That made 21-minute slider seeks look in-window. Use the
 * playlist EXTINF sum and the buffer, and only trust seekable when it matches.
 */
export function nativeGeneratedEndSec(video: HTMLVideoElement, listedSec?: number): number | undefined {
  const listed = listedSec != null && Number.isFinite(listedSec) && listedSec > 0 && listedSec < 86_400 ? listedSec : undefined;
  const buf =
    video.buffered.length > 0 && Number.isFinite(video.buffered.end(video.buffered.length - 1)) && video.buffered.end(video.buffered.length - 1) > 0
      ? video.buffered.end(video.buffered.length - 1)
      : undefined;
  const seek =
    video.seekable.length > 0 && Number.isFinite(video.seekable.end(video.seekable.length - 1))
      ? video.seekable.end(video.seekable.length - 1)
      : undefined;
  const seekOk = seek != null && seek > 0 && seek < 86_400 && (buf != null ? seek <= buf + 15 : seek <= 600);
  const candidates = [listed, buf, seekOk ? seek : undefined].filter((n): n is number => n != null);
  if (candidates.length === 0) return undefined;
  return Math.min(...candidates);
}

/** True if the movie timestamp can be seeked inside the current HLS/MSE window. */
export function canSeekInWindow(opts: {
  targetMs: number;
  originMs: number;
  seekableStartSec?: number;
  seekableEndSec?: number;
  /** EVENT playlist / buffer edge. Pinned movie duration must not be used here. */
  generatedEndSec?: number;
  /** EVENT/live playlists on iOS report a sliding seekable start near the frontier. */
  ignoreSeekableStart?: boolean;
}): boolean {
  const { targetMs, originMs, seekableStartSec, seekableEndSec, generatedEndSec, ignoreSeekableStart } = opts;
  if (targetMs < originMs - 500) return false;
  if (generatedEndSec != null && Number.isFinite(generatedEndSec) && targetMs > originMs + generatedEndSec * 1000 + 8000) {
    return false;
  }
  let endSec = seekableEndSec;
  // iOS EVENT often reports a movie-length seekable; without a generated edge that is a lie.
  if (ignoreSeekableStart && generatedEndSec == null && endSec != null && endSec > 600) {
    endSec = undefined;
  }
  if (seekableStartSec == null || endSec == null || !Number.isFinite(seekableStartSec) || !Number.isFinite(endSec)) {
    return Math.abs(targetMs - originMs) <= 2000;
  }
  const start = ignoreSeekableStart ? originMs : originMs + seekableStartSec * 1000;
  const end = originMs + endSec * 1000;
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
