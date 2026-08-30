/**
 * WebKit MMS opens if remote playback is disabled OR an AirPlay-compatible
 * alternate <source> exists (https://webkit.org/blog/15036/).
 * hls.js attaches a blob/mp4 source first; the m3u8 sibling is AirPlay only.
 */

export function disableRemotePlaybackForMms(video: HTMLVideoElement) {
  video.disableRemotePlayback = true;
}

export function addAirPlayAlternate(video: HTMLVideoElement, playlist: string) {
  if (video.querySelector("source[data-vd-airplay]")) return;
  const src = document.createElement("source");
  src.setAttribute("data-vd-airplay", "1");
  src.type = "application/x-mpegURL";
  src.src = playlist;
  video.appendChild(src);
  video.setAttribute("x-webkit-airplay", "allow");
  // Sibling is now present, so MMS can stay open with remote playback enabled.
  video.disableRemotePlayback = false;
  video.removeAttribute("disableremoteplayback");
}

export function sourceOrder(video: HTMLVideoElement): string[] {
  return [...video.querySelectorAll("source")].map((el) => el.type || el.getAttribute("type") || "");
}
