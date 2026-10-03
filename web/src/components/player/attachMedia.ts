import { nativeHlsSupported, sessionUrl } from "@/api/profile";
import { allowInlinePlayback, isIOSDevice } from "@/lib/device";
import { disableRemotePlaybackForMms, stripAlternateSources } from "@/playback/airplay";
import { isFsWindow, noteAttach, noteCurrentTimeWrite, noteHlsError, setAttachMeta } from "@/playback/attachTrace";
import { movieDurationSec, pinOpenMediaSource } from "@/playback/mediaDuration";
import { selectEngine, type PlaybackEngine } from "@/playback/policy";
import { inspectPlaylistBody, playlistReadiness } from "@/playback/playlistInspect";
import { eventPlaylistHlsSync } from "@/playback/hlsLiveSync";
import { captureSeekHold, seekHoldAction, shouldReplaceForGenerated } from "@/playback/seekHold";
import { nativeGeneratedEndSec } from "./seekWindow";
import { bufferCacheEnabled, directBufferCache, hlsBufferCache, type BufferCacheTitle, type CacheStats } from "./bufferCache";
import type { PlaybackSession } from "@/types/api.gen";

export type AttachHandle = {
  engine: PlaybackEngine;
  /** Waits until enough is cached ahead to start without stalling (buffer cache sessions). */
  prebuffer?: (maxMs: number) => Promise<void>;
  /** The buffer cache's state, for the stats overlay and the seek bar. */
  stats?: () => CacheStats;
  generatedEndSec?: () => number | undefined;
  destroy: () => void;
};

export class SessionGoneError extends Error {
  constructor() {
    super("SESSION_GONE");
    this.name = "SessionGoneError";
  }
}

/** The browser could not decode the original video the source sent; retry with H.264. */
export class CodecFallbackError extends Error {
  constructor(detail: string) {
    super(`CODEC_FALLBACK: ${detail}`);
    this.name = "CodecFallbackError";
  }
}

export type AttachOptions = {
  cacheTitle?: BufferCacheTitle;
  /** Where playback starts (seconds), so a resumed external stream loads from there, not from the beginning. */
  startSec?: number;
  /** The original (non-H.264) video failed after playback started. */
  onCodecFallback?: (detail: string) => void;
};

const CODEC_TAGS: Record<string, string[]> = { h264: ["avc1", "avc3"], hevc: ["hvc1", "hev1"], av1: ["av01"] };

function levelCodec(codecs: string | undefined): string {
  const tag = (codecs ?? "").split(".")[0].toLowerCase();
  return Object.keys(CODEC_TAGS).find((c) => CODEC_TAGS[c].includes(tag)) ?? "";
}

export async function attachSession(
  video: HTMLVideoElement,
  session: PlaybackSession,
  onGone: () => void,
  onEngine?: (engine: PlaybackEngine) => void,
  onBeyondGenerated?: (movieMs: number) => void,
  onFatal?: (detail: string) => void,
  opts: AttachOptions = {},
): Promise<AttachHandle> {
  const cacheTitle = opts.cacheTitle;
  let aborted = false;
  const gone = () => {
    if (!aborted) onGone();
  };

  prepareVideo(video);
  noteAttach(video, "prepareVideo");

  if (session.delivery === "direct") {
    const src = sessionUrl(session.urls, "file", "direct", "media");
    if (!src) throw new Error("session missing urls.file");
    const buffer = bufferCacheEnabled(session, cacheTitle) ? await directBufferCache(video, session, cacheTitle, src) : null;
    video.src = buffer?.url ?? src;
    if (buffer) noteAttach(video, "stream_cache_direct");
    onEngine?.("direct");
    return {
      engine: "direct",
      prebuffer: buffer?.prebuffer,
      stats: buffer?.stats,
      destroy() {
        aborted = true;
        buffer?.destroy();
        video.removeAttribute("src");
        video.load();
      },
    };
  }

  const playlist = sessionUrl(session.urls, "hls", "playlist", "index", "master");
  if (!playlist) throw new Error("session missing HLS url");
  const remote = session.decision?.mode === "remote";
  const playlistMeta = await waitForPlaylist(playlist, remote);
  const vodOnDemand = Boolean(session.vod_ondemand);
  setAttachMeta(video, {
    playlistUrl: playlist,
    playlistType: playlistMeta.type,
    playlistDurationMs: playlistMeta.durationMs,
  });
  noteAttach(video, "playlist_ready", `type=${playlistMeta.type || "?"} listed_ms=${playlistMeta.durationMs ?? "?"} vod=${vodOnDemand}`);

  const { default: Hls } = await import("hls.js");
  const engine = selectEngine(session.delivery, {
    hlsJsSupported: Hls.isSupported(),
    nativeHls: nativeHlsSupported(),
  }, isIOSDevice(), session.hls_attach);
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
    return attachWithHls(video, playlist, Hls, session, () => aborted, gone, onBeyondGenerated, onFatal, remote, opts);
  }
  return attachNativeHls(video, playlist, () => aborted, gone, playlistMeta.durationMs, remote, onFatal);
}

function prepareVideo(video: HTMLVideoElement) {
  // 67e1f88: iOS uses Safari's native controls (that fullscreen button works).
  video.controls = isIOSDevice();
  allowInlinePlayback(video, true);
  stripAlternateSources(video);
}

async function attachWithHls(
  video: HTMLVideoElement,
  playlist: string,
  Hls: typeof import("hls.js").default,
  session: PlaybackSession,
  isAborted: () => boolean,
  gone: () => void,
  onBeyondGenerated?: (movieMs: number) => void,
  onFatal?: (detail: string) => void,
  remote?: boolean,
  opts: AttachOptions = {},
): Promise<AttachHandle> {
  const movieSec = movieDurationSec(session.duration_ms);
  const cacheTitle = opts.cacheTitle;
  // External sources serve the whole title from 0, so a resume starts loading at the resume point.
  const startSec = session.source?.startsWith("jellyfin:") && (opts.startSec ?? 0) > 2.5 ? (opts.startSec as number) : 0;
  const buffer = bufferCacheEnabled(session, cacheTitle) ? hlsBufferCache(video, session, cacheTitle, Hls, startSec) : null;
  // The source sends its original video when this device decodes it. Jellyfin
  // also lists an H.264 re-encode of the same size, which is what stalls, so
  // the player stays on the original and falls back by asking for H.264 only.
  const original = session.remote_video?.copy && session.remote_video.codec !== "h264" ? session.remote_video.codec : "";
  let failCodec: (err: Error) => void = () => undefined;
  const codecFailed = new Promise<never>((_, reject) => {
    failCodec = reject;
  });
  codecFailed.catch(() => undefined);
  let codecFallbackSent = false;
  const codecFallback = (detail: string) => {
    if (codecFallbackSent) return;
    codecFallbackSent = true;
    noteAttach(video, "codec_fallback", detail);
    if (attached) opts.onCodecFallback?.(detail);
    else failCodec(new CodecFallbackError(detail));
  };
  const hls = new Hls({
    enableWorker: true,
    preferManagedMediaSource: true,
    startPosition: startSec,
    ...eventPlaylistHlsSync(),
    maxBufferLength: 30,
    maxMaxBufferLength: 90,
    backBufferLength: 900,
    // The buffer cache keeps minutes on disk and hands segments over at
    // once, so MSE only holds a few seconds. Chrome's SourceBuffer holds
    // about 150 MB, which 30 seconds of an original 4K video (55 MB a
    // segment) overflows: the append fails and playback stops at the gap.
    ...(buffer
      ? { backBufferLength: 4, maxBufferLength: 15, maxMaxBufferLength: 30, maxBufferSize: 60 * 1000 * 1000, maxBufferHole: 0.5 }
      : {}),
    xhrSetup(xhr) {
      xhr.withCredentials = true;
    },
    ...(buffer ? { fLoader: buffer.fLoader } : {}),
  });
  if (buffer) {
    buffer.bind(hls);
    noteAttach(video, "stream_cache_hls");
  }
  let fatalErr: Error | null = null;
  let attached = false;
  let mediaRecovered = false;
  const onHlsError = (_e: unknown, data: import("hls.js").ErrorData) => {
    noteHlsError(video, {
      type: data.type,
      details: data.details,
      fatal: data.fatal,
      reason: data.reason,
      error: data.error,
      response: data.response,
      frag: data.frag
        ? {
            sn: typeof data.frag.sn === "number" ? data.frag.sn : undefined,
            start: data.frag.start,
            duration: data.frag.duration,
            url: data.frag.url,
            relurl: data.frag.relurl,
          }
        : undefined,
    });
    if (!data.fatal) return;
    fatalErr = new Error(data.details || data.type || "hls error");
    if (data.response?.code === 410) {
      if (remote) {
        onFatal?.("This playback stopped because it started somewhere else.");
        return;
      }
      gone();
      return;
    }
    const playingOriginal = original !== "" && levelCodec(hls.levels[hls.currentLevel]?.videoCodec ?? hls.levels[0]?.videoCodec) === original;
    if (playingOriginal && data.type === Hls.ErrorTypes.MEDIA_ERROR && (mediaRecovered || !attached)) {
      codecFallback(data.details || "media error");
      return;
    }
    if (!attached || isAborted()) return;
    if (data.type === Hls.ErrorTypes.MEDIA_ERROR && !mediaRecovered) {
      mediaRecovered = true;
      noteAttach(video, "hls:recoverMediaError", data.details);
      hls.recoverMediaError();
      return;
    }
    const code = data.response?.code ? ` (HTTP ${data.response.code})` : "";
    onFatal?.(`${data.details || data.type || "hls error"}${code}`);
  };
  hls.on(Hls.Events.ERROR, onHlsError);
  hls.on(Hls.Events.MEDIA_ATTACHING, () => noteAttach(video, "hls:MEDIA_ATTACHING"));
  hls.on(Hls.Events.MEDIA_ATTACHED, () => noteAttach(video, "hls:MEDIA_ATTACHED"));
  hls.on(Hls.Events.MEDIA_DETACHING, () => noteAttach(video, "hls:MEDIA_DETACHING"));
  hls.on(Hls.Events.MANIFEST_LOADING, () => noteAttach(video, "hls:MANIFEST_LOADING"));
  hls.on(Hls.Events.MANIFEST_PARSED, () => noteAttach(video, "hls:MANIFEST_PARSED"));
  if (original) {
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      // hls.js drops variants MSE cannot play, so a missing original means
      // the browser overstated its decoder.
      const idx = hls.levels.findIndex((l) => levelCodec(l.videoCodec) === original);
      if (idx < 0) {
        codecFallback(`no playable ${original} variant`);
        return;
      }
      hls.currentLevel = idx;
      noteAttach(video, "level_locked", `${original} level=${idx}`);
    });
  }
  const hlsQuiet = (name: string, extra?: string) => {
    if (!isFsWindow(video) && !name.includes("FLUSH") && name !== "FRAG_CHANGED") return;
    noteAttach(video, `hls:${name}`, extra);
  };
  hls.on(Hls.Events.FRAG_LOADING, (_e, data) => hlsQuiet("FRAG_LOADING", `sn=${data.frag?.sn}`));
  hls.on(Hls.Events.FRAG_LOADED, (_e, data) => hlsQuiet("FRAG_LOADED", `sn=${data.frag?.sn}`));
  hls.on(Hls.Events.FRAG_BUFFERED, (_e, data) => hlsQuiet("FRAG_BUFFERED", `sn=${data.frag?.sn}`));
  hls.on(Hls.Events.FRAG_CHANGED, (_e, data) => noteAttach(video, "hls:FRAG_CHANGED", `sn=${data.frag?.sn} start=${data.frag?.start}`));
  hls.on(Hls.Events.LEVEL_LOADING, () => hlsQuiet("LEVEL_LOADING"));
  let playlistEdge = 0;
  let playlistEndlist = false;
  let seekHold: number | null = null;
  let seekHoldAt = 0;
  let lastKeepAt = 0;
  let lastPollEdge = -1;
  let holdPoll = 0;
  const stopHoldPoll = () => {
    if (!holdPoll) return;
    window.clearInterval(holdPoll);
    holdPoll = 0;
  };
  const runSeekHold = (why: string) => {
    const now = video.currentTime;
    const action = seekHoldAction({
      requestedSec: seekHold,
      nowSec: now,
      playlistEdgeSec: playlistEdge,
      movieSec,
      endlist: playlistEndlist,
      heldForMs: seekHoldAt ? Date.now() - seekHoldAt : 0,
    });
    if (action === "keep" && seekHold != null && Date.now() - lastKeepAt > 280) {
      lastKeepAt = Date.now();
      noteCurrentTimeWrite(video, seekHold, `seek_hold_keep:${why}`, session.id);
      video.currentTime = seekHold;
      noteAttach(video, "seek_hold_keep", `t=${seekHold} edge=${playlistEdge} via=${why}`);
    } else if (action === "apply" && seekHold != null) {
      const target = seekHold;
      seekHold = null;
      stopHoldPoll();
      noteCurrentTimeWrite(video, target, `seek_hold_apply:${why}`, session.id);
      video.currentTime = target;
      hls.startLoad(target);
      noteAttach(video, "seek_hold_apply", `t=${target} edge=${playlistEdge}`);
    } else if (action === "timeout" || action === "clear") {
      noteAttach(video, `seek_hold_${action}`, `hold=${seekHold} edge=${playlistEdge}`);
      seekHold = null;
      stopHoldPoll();
    }
  };
  let pollBusy = false;
  const pollHoldPlaylist = async () => {
    if (seekHold == null) {
      stopHoldPoll();
      return;
    }
    if (pollBusy) return;
    pollBusy = true;
    try {
      const res = await fetch(playlist, { credentials: "include", cache: "no-store" });
      if (!res.ok) return;
      const text = await res.text();
      if (playlistReadiness(text) === "master") return;
      const snap = inspectPlaylistBody(text, "seek_hold_poll");
      playlistEdge = snap.sumExtinfSec;
      playlistEndlist = snap.endlist;
      if (snap.sumExtinfSec !== lastPollEdge) {
        lastPollEdge = snap.sumExtinfSec;
        noteAttach(video, "seek_hold_poll", `edge=${snap.sumExtinfSec} segs=${snap.segmentCount} hold=${seekHold}`);
      }
      runSeekHold("playlist_poll");
    } catch {
      /* keep waiting */
    } finally {
      pollBusy = false;
    }
  };
  const startHoldPoll = () => {
    if (holdPoll) return;
    void pollHoldPlaylist();
    holdPoll = window.setInterval(() => {
      void pollHoldPlaylist();
    }, 400);
  };
  let farSeekAt = 0;
  const onAvkitSeeking = () => {
    const now = video.currentTime;
    if (onBeyondGenerated && shouldReplaceForGenerated(now, playlistEdge) && Date.now() - farSeekAt > 400) {
      farSeekAt = Date.now();
      const origin = session.seekable_from_ms ?? 0;
      noteAttach(video, "beyond_generated_replace", `t=${now} edge=${playlistEdge}`);
      onBeyondGenerated(origin + now * 1000);
      return;
    }
    const captured = captureSeekHold(now, playlistEdge, movieSec);
    if (captured != null) {
      seekHold = captured;
      seekHoldAt = Date.now();
      noteAttach(video, "seek_hold_begin", `t=${captured} edge=${playlistEdge}`);
      startHoldPoll();
      return;
    }
    runSeekHold("seeking");
  };
  video.addEventListener("seeking", onAvkitSeeking);
  hls.on(Hls.Events.LEVEL_LOADED, (_e, data) => {
    playlistEdge = data.details?.edge ?? data.details?.totalduration ?? playlistEdge;
    playlistEndlist = Boolean(data.details && data.details.live === false);
    noteAttach(video, "hls:LEVEL_LOADED", `details=${data.details?.totalduration ?? ""} edge=${playlistEdge} endSN=${data.details?.endSN ?? ""}`);
    runSeekHold("LEVEL_LOADED");
  });
  hls.on(Hls.Events.BUFFER_APPENDING, () => hlsQuiet("BUFFER_APPENDING"));
  hls.on(Hls.Events.BUFFER_APPENDED, () => hlsQuiet("BUFFER_APPENDED"));
  hls.on(Hls.Events.BUFFER_FLUSHING, (_e, data) =>
    noteAttach(video, "hls:BUFFER_FLUSHING", `${data.startOffset ?? ""}-${data.endOffset ?? ""}`),
  );
  hls.on(Hls.Events.BUFFER_FLUSHED, () => noteAttach(video, "hls:BUFFER_FLUSHED"));
  let mediaSourceRef: MediaSource | null = null;
  const mediaSourceOf = () => {
    if (mediaSourceRef) return mediaSourceRef;
    const fromHls = (hls as unknown as { mediaSource?: MediaSource | null }).mediaSource;
    if (fromHls) return fromHls;
    const obj = video.srcObject;
    if (obj && typeof (obj as MediaSource).duration === "number") return obj as MediaSource;
    return null;
  };
  const pinMovieDuration = (why: string) => {
    if (movieSec == null) return;
    const ms = mediaSourceOf();
    if (pinOpenMediaSource(ms, movieSec)) {
      setAttachMeta(video, { pinnedDurationSec: movieSec });
      noteAttach(video, "mms_duration_pin", `${why} sec=${movieSec} video.duration=${video.duration}`);
    }
  };
  const hookMediaSource = () => {
    const ms = mediaSourceOf();
    if (!ms?.addEventListener || (ms as { _vdHooked?: boolean })._vdHooked) return;
    (ms as { _vdHooked?: boolean })._vdHooked = true;
    ms.addEventListener("sourceopen", () => {
      noteAttach(video, "sourceopen", ms.readyState);
      pinMovieDuration("sourceopen");
    });
    ms.addEventListener("sourceclose", () => noteAttach(video, "sourceclose", ms.readyState));
    ms.addEventListener("sourceended", () => noteAttach(video, "sourceended", ms.readyState));
    ms.addEventListener("startstreaming", () => noteAttach(video, "mms:startstreaming"));
    ms.addEventListener("endstreaming", () => noteAttach(video, "mms:endstreaming"));
    pinMovieDuration("hook");
  };
  hls.on(Hls.Events.MEDIA_ATTACHING, (_e, data) => {
    if (data.mediaSource) mediaSourceRef = data.mediaSource;
    hookMediaSource();
  });
  hls.on(Hls.Events.MEDIA_ATTACHED, (_e, data) => {
    if (data.mediaSource) mediaSourceRef = data.mediaSource;
    hookMediaSource();
    pinMovieDuration("MEDIA_ATTACHED");
  });
  hls.on(Hls.Events.LEVEL_LOADED, () => pinMovieDuration("LEVEL_LOADED"));
  hls.on(Hls.Events.BUFFER_APPENDED, () => pinMovieDuration("BUFFER_APPENDED"));
  const onDurPin = () => pinMovieDuration("durationchange");
  video.addEventListener("durationchange", onDurPin);
  video.addEventListener("loadedmetadata", () => noteAttach(video, "video:loadedmetadata", `duration=${video.duration}`), { once: true });
  // MMS sourceopen requires disableRemotePlayback. Do not add an HLS
  // <source> sibling: Safari plays that inline and fights hls.js.
  disableRemotePlaybackForMms(video);
  setAttachMeta(video, { airplayPolicy: "skipped_intentional_dual_owner" });
  noteAttach(video, "disableRemotePlayback", "true");
  noteAttach(video, "airplay_sibling", "skipped_intentional_dual_owner");
  if (movieSec != null) {
    hls.attachMedia({ media: video, overrides: { duration: movieSec } });
    setAttachMeta(video, { pinnedDurationSec: movieSec });
    noteAttach(video, "hls.attachMedia", `duration_override=${movieSec}`);
  } else {
    hls.attachMedia(video);
    noteAttach(video, "hls.attachMedia");
  }
  hls.loadSource(playlist);
  noteAttach(video, "hls.loadSource");
  try {
    const buffered = waitHlsBuffered(
      hls as unknown as { on: (ev: string, cb: () => void) => void; off: (ev: string, cb: () => void) => void },
      video,
      () => isAborted() || codecFallbackSent,
      Hls,
    );
    buffered.catch(() => undefined);
    await Promise.race([buffered, codecFailed]);
  } catch (err) {
    buffer?.destroy();
    hls.destroy();
    if (err instanceof CodecFallbackError) throw err;
    if (fatalErr) throw fatalErr;
    throw err;
  }
  attached = true;
  return {
    engine: "hlsjs",
    prebuffer: buffer?.prebuffer,
    stats: buffer?.stats,
    generatedEndSec: () => playlistEdge,
    destroy() {
      stopHoldPoll();
      video.removeEventListener("seeking", onAvkitSeeking);
      video.removeEventListener("durationchange", onDurPin);
      buffer?.destroy();
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
  listedDurationMs?: number,
  remote?: boolean,
  onFatal?: (detail: string) => void,
): Promise<AttachHandle> {
  video.disableRemotePlayback = false;
  video.removeAttribute("disableremoteplayback");
  video.setAttribute("x-webkit-airplay", "allow");
  video.controls = true;
  video.src = playlist;
  noteAttach(video, "native_src");
  const listedSec = listedDurationMs != null && listedDurationMs > 0 ? listedDurationMs / 1000 : undefined;
  const generatedEndSec = () => nativeGeneratedEndSec(video, listedSec);
  const onError = () => {
    void fetch(playlist, { credentials: "include" }).then((res) => {
      if (res.status === 410) {
        if (remote) onFatal?.("This playback stopped because it started somewhere else.");
        else gone();
      }
    }).catch(() => undefined);
  };
  video.addEventListener("error", onError);
  try {
    await waitCanPlay(video, isAborted);
  } catch (err) {
    video.removeEventListener("error", onError);
    throw err;
  }
  return {
    engine: "native-hls",
    generatedEndSec,
    destroy() {
      video.removeEventListener("error", onError);
      video.removeAttribute("src");
      video.load();
    },
  };
}

async function waitForPlaylist(url: string, remote = false): Promise<{ type?: string; durationMs?: number; endlist?: boolean }> {
  const deadline = Date.now() + 50_000;
  while (Date.now() < deadline) {
    const res = await fetch(url, { credentials: "include" });
    if (res.status === 410) {
      if (remote) throw new Error("This playback stopped because it started somewhere else.");
      throw new SessionGoneError();
    }
    if (res.ok) {
      const text = await res.text();
      const ready = playlistReadiness(text);
      if (ready === "master") {
        return { type: res.headers.get("X-VD-Playlist-Type") || undefined };
      }
      if (ready === "media") {
        const listed = Number(res.headers.get("X-VD-Playlist-Duration-Ms"));
        const snap = inspectPlaylistBody(text, "wait", res.headers);
        return {
          type: res.headers.get("X-VD-Playlist-Type") || snap.type || undefined,
          durationMs: Number.isFinite(listed) && listed > 0 ? listed : undefined,
          endlist: snap.endlist,
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
