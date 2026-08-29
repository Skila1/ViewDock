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
  await waitForPlaylist(playlist, gone);

  if (nativeHlsSupported()) {
    video.src = playlist;
    const onError = () => {
      void fetch(playlist, { credentials: "include" }).then((res) => {
        if (res.status === 410) gone();
      }).catch(() => undefined);
    };
    video.addEventListener("error", onError);
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
      xhrSetup(xhr) {
        xhr.withCredentials = true;
      },
    });
    hls.loadSource(playlist);
    hls.attachMedia(video);
    hls.on(Hls.Events.ERROR, (_e, data) => {
      if (data.fatal && data.response?.code === 410) gone();
    });
    return {
      destroy() {
        aborted = true;
        hls.destroy();
      },
    };
  }

  throw new Error("HLS is not supported in this browser");
}

async function waitForPlaylist(url: string, gone: () => void): Promise<void> {
  const deadline = Date.now() + 50_000;
  while (Date.now() < deadline) {
    const res = await fetch(url, { credentials: "include" });
    if (res.status === 410) {
      throw new SessionGoneError();
    }
    if (res.ok) {
      const text = await res.text();
      if (text.trimStart().startsWith("#EXTM3U")) {
        return;
      }
    }
    await new Promise((r) => setTimeout(r, 800));
  }
  throw new Error("Stream is still starting. Retry in a moment.");
}
