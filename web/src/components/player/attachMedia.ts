import { nativeHlsSupported, sessionUrl } from "@/api/profile";
import type { PlaybackSession } from "@/types/api.gen";

export type AttachHandle = {
  destroy: () => void;
};

export class SessionGoneError extends Error {
  constructor() {
    super("SESSION_GONE");
    this.name = "SessionGoneError";
  }
}

export async function attachSession(
  video: HTMLVideoElement,
  session: PlaybackSession,
  onGone: () => void,
): Promise<AttachHandle> {
  let aborted = false;
  const gone = () => {
    if (!aborted) onGone();
  };

  if (session.delivery === "direct") {
    const src = sessionUrl(session.urls, "file", "direct", "media");
    if (!src) throw new Error("session missing urls.file");
    video.src = src;
    return {
      destroy() {
        aborted = true;
        video.removeAttribute("src");
        video.load();
      },
    };
  }

  const playlist = sessionUrl(session.urls, "hls", "playlist", "index", "master");
  if (!playlist) throw new Error("session missing HLS url");
  await waitForPlaylist(playlist);

  if (nativeHlsSupported()) {
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "");
    video.setAttribute("x-webkit-airplay", "allow");
    video.playsInline = true;
    video.controls = true;
    video.src = playlist;
    const onError = () => {
      void fetch(playlist, { credentials: "include" }).then((res) => {
        if (res.status === 410) gone();
      }).catch(() => undefined);
    };
    video.addEventListener("error", onError);
    try {
      await waitCanPlay(video, () => aborted);
    } catch (err) {
      video.removeEventListener("error", onError);
      throw err;
    }
    return {
      destroy() {
        aborted = true;
        video.removeEventListener("error", onError);
        video.removeAttribute("src");
        video.load();
      },
    };
  }

  const { default: Hls } = await import("hls.js");
  if (Hls.isSupported()) {
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      // EVENT playlists look live while ffmpeg is still writing. Play from
      // the start instead of chasing the transcode frontier.
      startPosition: 0,
      liveDurationInfinity: false,
      liveSyncDurationCount: 30,
      liveMaxLatencyDurationCount: Infinity,
      maxLiveSyncPlaybackRate: 1,
      maxBufferLength: 30,
      maxMaxBufferLength: 90,
      // Keep enough of the window that a short skip-back stays in MSE.
      backBufferLength: 900,
      xhrSetup(xhr) {
        xhr.withCredentials = true;
      },
    });
    let fatalErr: Error | null = null;
    const onHlsError = (_e: unknown, data: { fatal?: boolean; type?: string; details?: string; response?: { code?: number } }) => {
      if (data.fatal && data.response?.code === 410) gone();
      if (data.fatal) {
        fatalErr = new Error(data.details || data.type || "hls error");
      }
    };
    hls.on(Hls.Events.ERROR, onHlsError);
    hls.attachMedia(video);
    hls.loadSource(playlist);
    try {
      await waitHlsBuffered(
        hls as unknown as { on: (ev: string, cb: () => void) => void; off: (ev: string, cb: () => void) => void },
        video,
        () => aborted,
        Hls,
      );
    } catch (err) {
      hls.destroy();
      if (fatalErr) throw fatalErr;
      throw err;
    }
    return {
      destroy() {
        aborted = true;
        hls.destroy();
      },
    };
  }

  throw new Error("HLS is not supported in this browser");
}

async function waitForPlaylist(url: string): Promise<void> {
  const deadline = Date.now() + 50_000;
  while (Date.now() < deadline) {
    const res = await fetch(url, { credentials: "include" });
    if (res.status === 410) {
      throw new SessionGoneError();
    }
    if (res.ok) {
      const text = await res.text();
      if (text.includes("#EXTINF") || /seg\d+\.(m4s|ts)/.test(text)) {
        return;
      }
    }
    await new Promise((r) => setTimeout(r, 800));
  }
  throw new Error("Stream is still starting. Retry in a moment.");
}

async function waitHlsBuffered(
  hls: { on: (ev: string, cb: () => void) => void; off: (ev: string, cb: () => void) => void },
  video: HTMLVideoElement,
  isAborted: () => boolean,
  HlsCtor: { Events: { FRAG_BUFFERED: string } },
): Promise<void> {
  if (video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA && video.buffered.length > 0) {
    return;
  }
  await new Promise<void>((resolve, reject) => {
    const deadline = window.setTimeout(() => {
      cleanup();
      reject(new Error("Stream is still starting. Retry in a moment."));
    }, 45_000);
    const ok = () => {
      cleanup();
      resolve();
    };
    const onAbort = window.setInterval(() => {
      if (!isAborted()) return;
      cleanup();
      reject(new SessionGoneError());
    }, 250);
    const cleanup = () => {
      window.clearTimeout(deadline);
      window.clearInterval(onAbort);
      video.removeEventListener("canplay", ok);
      hls.off(HlsCtor.Events.FRAG_BUFFERED, ok);
    };
    video.addEventListener("canplay", ok);
    hls.on(HlsCtor.Events.FRAG_BUFFERED, ok);
  });
}

async function waitCanPlay(video: HTMLVideoElement, isAborted: () => boolean): Promise<void> {
  if (video.readyState >= HTMLMediaElement.HAVE_FUTURE_DATA && video.buffered.length > 0) {
    return;
  }
  await new Promise<void>((resolve, reject) => {
    const deadline = window.setTimeout(() => {
      cleanup();
      reject(new Error("Stream is still starting. Retry in a moment."));
    }, 45_000);
    const ok = () => {
      cleanup();
      resolve();
    };
    const onAbort = window.setInterval(() => {
      if (!isAborted()) return;
      cleanup();
      reject(new SessionGoneError());
    }, 250);
    const cleanup = () => {
      window.clearTimeout(deadline);
      window.clearInterval(onAbort);
      video.removeEventListener("canplay", ok);
    };
    video.addEventListener("canplay", ok);
  });
}
