import { nativeHlsSupported, sessionUrl } from "@/api/profile";
import { disableRemotePlaybackForMms, stripAlternateSources } from "@/playback/airplay";
import { noteAttach, setAttachMeta } from "@/playback/attachTrace";
import { selectEngine, type PlaybackEngine } from "@/playback/policy";
import type { PlaybackSession } from "@/types/api.gen";

export type AttachHandle = {
  engine: PlaybackEngine;
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
  onEngine?: (engine: PlaybackEngine) => void,
): Promise<AttachHandle> {
  let aborted = false;
  const gone = () => {
    if (!aborted) onGone();
  };

  prepareVideo(video);
  noteAttach(video, "prepareVideo");

  if (session.delivery === "direct") {
    const src = sessionUrl(session.urls, "file", "direct", "media");
    if (!src) throw new Error("session missing urls.file");
    video.src = src;
    onEngine?.("direct");
    return {
      engine: "direct",
      destroy() {
        aborted = true;
        video.removeAttribute("src");
        video.load();
      },
    };
  }

  const playlist = sessionUrl(session.urls, "hls", "playlist", "index", "master");
  if (!playlist) throw new Error("session missing HLS url");
  const playlistMeta = await waitForPlaylist(playlist);
  setAttachMeta(video, {
    playlistType: playlistMeta.type,
    playlistDurationMs: playlistMeta.durationMs,
  });
  noteAttach(video, "playlist_ready", `type=${playlistMeta.type || "?"} listed_ms=${playlistMeta.durationMs ?? "?"}`);

  const { default: Hls } = await import("hls.js");
  const engine = selectEngine(session.delivery, {
    hlsJsSupported: Hls.isSupported(),
    nativeHls: nativeHlsSupported(),
  });
  const reason = `${engine} hlsJsSupported=${Hls.isSupported()} nativeHls=${nativeHlsSupported()} hls_attach=${session.hls_attach ?? ""}`;
  setAttachMeta(video, {
    engineReason: reason,
    hlsJsSupported: Hls.isSupported(),
    mmsAvailable: typeof (globalThis as { ManagedMediaSource?: unknown }).ManagedMediaSource !== "undefined",
    mseAvailable: typeof MediaSource !== "undefined",
  });
  noteAttach(video, "engine_selected", reason);
  onEngine?.(engine);

  if (engine === "hlsjs") {
    return attachWithHls(video, playlist, Hls, () => aborted, gone);
  }
  return attachNativeHls(video, playlist, () => aborted, gone);
}

function prepareVideo(video: HTMLVideoElement) {
  video.controls = false;
  video.playsInline = true;
  video.setAttribute("playsinline", "");
  video.setAttribute("webkit-playsinline", "");
  stripAlternateSources(video);
}

async function attachWithHls(
  video: HTMLVideoElement,
  playlist: string,
  Hls: typeof import("hls.js").default,
  isAborted: () => boolean,
  gone: () => void,
): Promise<AttachHandle> {
  const hls = new Hls({
    enableWorker: true,
    preferManagedMediaSource: true,
    lowLatencyMode: false,
    startPosition: 0,
    liveDurationInfinity: false,
    liveSyncDurationCount: 30,
    liveMaxLatencyDurationCount: Infinity,
    maxLiveSyncPlaybackRate: 1,
    maxBufferLength: 30,
    maxMaxBufferLength: 90,
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
  hls.on(Hls.Events.MEDIA_ATTACHING, () => noteAttach(video, "hls:MEDIA_ATTACHING"));
  hls.on(Hls.Events.MEDIA_ATTACHED, () => noteAttach(video, "hls:MEDIA_ATTACHED"));
  hls.on(Hls.Events.MANIFEST_LOADING, () => noteAttach(video, "hls:MANIFEST_LOADING"));
  hls.on(Hls.Events.MANIFEST_PARSED, () => noteAttach(video, "hls:MANIFEST_PARSED"));
  const hookMediaSource = () => {
    const ms = (hls as unknown as { mediaSource?: EventTarget & { readyState?: string } }).mediaSource;
    if (!ms?.addEventListener || (ms as { _vdHooked?: boolean })._vdHooked) return;
    (ms as { _vdHooked?: boolean })._vdHooked = true;
    ms.addEventListener("sourceopen", () => noteAttach(video, "sourceopen", ms.readyState));
    ms.addEventListener("sourceclose", () => noteAttach(video, "sourceclose", ms.readyState));
    ms.addEventListener("sourceended", () => noteAttach(video, "sourceended", ms.readyState));
  };
  hls.on(Hls.Events.MEDIA_ATTACHING, hookMediaSource);
  hls.on(Hls.Events.MEDIA_ATTACHED, hookMediaSource);
  video.addEventListener("loadedmetadata", () => noteAttach(video, "video:loadedmetadata", `duration=${video.duration}`), { once: true });
  // MMS sourceopen requires disableRemotePlayback. Do not add an HLS
  // <source> sibling — Safari plays that inline and fights hls.js.
  disableRemotePlaybackForMms(video);
  setAttachMeta(video, { airplayPolicy: "skipped_intentional_dual_owner" });
  noteAttach(video, "disableRemotePlayback", "true");
  noteAttach(video, "airplay_sibling", "skipped_intentional_dual_owner");
  hls.attachMedia(video);
  noteAttach(video, "hls.attachMedia");
  hls.loadSource(playlist);
  noteAttach(video, "hls.loadSource");
  try {
    await waitHlsBuffered(
      hls as unknown as { on: (ev: string, cb: () => void) => void; off: (ev: string, cb: () => void) => void },
      video,
      isAborted,
      Hls,
    );
  } catch (err) {
    hls.destroy();
    if (fatalErr) throw fatalErr;
    throw err;
  }
  return {
    engine: "hlsjs",
    destroy() {
      hls.destroy();
      video.removeAttribute("src");
      video.querySelectorAll("source").forEach((el) => el.remove());
      video.load();
    },
  };
}

async function attachNativeHls(
  video: HTMLVideoElement,
  playlist: string,
  isAborted: () => boolean,
  gone: () => void,
): Promise<AttachHandle> {
  video.setAttribute("x-webkit-airplay", "allow");
  video.src = playlist;
  const onError = () => {
    void fetch(playlist, { credentials: "include" }).then((res) => {
      if (res.status === 410) gone();
    }).catch(() => undefined);
  };
  video.addEventListener("error", onError);
  try {
    await waitCanPlay(video, isAborted);
    if (video.currentTime > 0.25) video.currentTime = 0;
  } catch (err) {
    video.removeEventListener("error", onError);
    throw err;
  }
  return {
    engine: "native-hls",
    destroy() {
      video.removeEventListener("error", onError);
      video.removeAttribute("src");
      video.load();
    },
  };
}

async function waitForPlaylist(url: string): Promise<{ type?: string; durationMs?: number }> {
  const deadline = Date.now() + 50_000;
  while (Date.now() < deadline) {
    const res = await fetch(url, { credentials: "include" });
    if (res.status === 410) {
      throw new SessionGoneError();
    }
    if (res.ok) {
      const text = await res.text();
      if (text.includes("#EXTINF") || /seg\d+\.(m4s|ts)/.test(text)) {
        const listed = Number(res.headers.get("X-VD-Playlist-Duration-Ms"));
        return {
          type: res.headers.get("X-VD-Playlist-Type") || undefined,
          durationMs: Number.isFinite(listed) && listed > 0 ? listed : undefined,
        };
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
