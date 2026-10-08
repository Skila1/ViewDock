/**
 * EVENT remux/transcode playlists grow like live, but they are VOD.
 * hls.js default liveMaxLatencyDurationCount is Infinity (never snap).
 * A count of 3 seeks the playhead to the writing edge once the user is
 * more than ~3 segments behind — including while paused.
 */
export function eventPlaylistHlsSync() {
  return {
    liveSyncDurationCount: 3,
    liveMaxLatencyDurationCount: Number.POSITIVE_INFINITY,
    maxLiveSyncPlaybackRate: 1,
    liveDurationInfinity: false,
    lowLatencyMode: false,
  };
}

export function liveSyncWouldSeek(opts: {
  currentTime: number;
  playlistEdgeSec: number;
  targetDurationSec: number;
  liveMaxLatencyDurationCount: number;
}): boolean {
  const maxLatency = opts.liveMaxLatencyDurationCount * opts.targetDurationSec;
  if (!Number.isFinite(maxLatency)) return false;
  return opts.currentTime < opts.playlistEdgeSec - maxLatency;
}
