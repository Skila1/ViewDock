import { nativeHlsSupported, sessionUrl } from "@/api/profile";
import type { PlaybackSession } from "@/types/api.gen";

export type AttachHandle = {
  destroy: () => void;
};

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
