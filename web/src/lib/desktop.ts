/** What ViewDock for Windows tells the pages it shows (desktop/app/preload.js). */
export type DesktopBridge = {
  version: string;
  platform: string;
  /** Megabytes of video the app's media buffer holds per stream. */
  mediaBufferMB: number;
};

/** The desktop app this page runs in, or null in a browser. */
export function desktopApp(): DesktopBridge | null {
  if (typeof window === "undefined") return null;
  const d = (window as { viewdockDesktop?: DesktopBridge }).viewdockDesktop;
  return d && typeof d.version === "string" && d.mediaBufferMB > 0 ? d : null;
}

/**
 * hls.js buffer sizes for streams the buffer cache feeds. A browser holds
 * about 150 MB of video per stream, so it keeps the playing segment and the
 * next; the desktop app holds far more, so it keeps a minute ahead and a few
 * seconds behind, within its media buffer.
 */
export function bufferedHlsConfig(desktop: DesktopBridge | null) {
  if (!desktop) {
    return { backBufferLength: 0, maxBufferLength: 5, maxMaxBufferLength: 8, maxBufferSize: 50 * 1000 * 1000, maxBufferHole: 0.5 };
  }
  const budget = desktop.mediaBufferMB * 1000 * 1000;
  return {
    backBufferLength: 10,
    maxBufferLength: 60,
    maxMaxBufferLength: 90,
    maxBufferSize: Math.round(budget * 0.6),
    maxBufferHole: 0.5,
  };
}
