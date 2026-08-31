/**
 * WebKit opens Managed Media Source only if remote playback is disabled
 * OR an AirPlay-compatible alternate <source> exists
 * (https://webkit.org/blog/15036/).
 *
 * We must NOT add an application/x-mpegURL sibling. Safari treats that
 * source as inline native HLS, not AirPlay-only. On ViewDock EVENT
 * playlists that produces a seg0.ts fetch storm, dual ownership with
 * hls.js, and a dead video element — so fullscreen never becomes available.
 *
 * MMS precondition is disableRemotePlayback = true for the hls.js attach.
 * Lift that flag in the same user gesture as webkitEnterFullscreen (see
 * allowAvkitRemotePlayback) or AVKit never presents. Restore it on exit.
 */

export function disableRemotePlaybackForMms(video: HTMLVideoElement) {
  video.disableRemotePlayback = true;
  video.setAttribute("disableremoteplayback", "");
  video.removeAttribute("x-webkit-airplay");
}

export function stripAlternateSources(video: HTMLVideoElement) {
  video.querySelectorAll("source[data-vd-hls], source[data-vd-airplay]").forEach((el) => el.remove());
}
