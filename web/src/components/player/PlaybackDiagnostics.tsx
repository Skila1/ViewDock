import { useEffect, useRef, useState } from "react";
import { noteAttach, noteDisplayingFsChange, noteMedia, readAttachTrace } from "@/playback/attachTrace";
import { inferDiagnosticOwner, type PlaybackEngine } from "@/playback/policy";
import type { PlaybackSession } from "@/types/api.gen";

type AppleVideo = HTMLVideoElement & {
  webkitDisplayingFullscreen?: boolean;
  webkitPresentationMode?: string;
  webkitSupportsFullscreen?: boolean;
};

type Props = {
  video: HTMLVideoElement | null;
  session: PlaybackSession | null;
  engine: PlaybackEngine | null;
  originMs: number;
};

function ranges(r: TimeRanges): string {
  const out: string[] = [];
  for (let i = 0; i < r.length; i++) {
    out.push(`${r.start(i).toFixed(2)}-${r.end(i).toFixed(2)}`);
  }
  return out.join(", ") || "none";
}

function durationClocks(
  video: HTMLVideoElement | null,
  session: PlaybackSession | null,
  trace: ReturnType<typeof readAttachTrace>,
) {
  const latest = trace.playlistSnaps[trace.playlistSnaps.length - 1];
  return {
    movie_duration_ms: session?.duration_ms ?? null,
    playlist_listed_sec: latest?.sumExtinfSec ?? null,
    playlist_segment_count: latest?.segmentCount ?? null,
    video_duration: video && Number.isFinite(video.duration) ? video.duration : video?.duration ?? null,
    seekable: video ? ranges(video.seekable) : "",
    pinned_media_duration_sec: trace.pinnedDurationSec ?? null,
    note: "movie_duration_ms is ffprobe. playlist_listed is generated HLS. video.duration/seekable should match the pinned movie length in AVKit. Playlist listed can still grow while remux runs.",
  };
}

function sourceDetails(video: HTMLVideoElement | null) {
  if (!video) return [];
  return [...video.querySelectorAll("source")].map((el) => ({
    type: el.type || el.getAttribute("type") || "",
    src: (el.getAttribute("src") || "").slice(0, 96),
    data_vd_hls: el.getAttribute("data-vd-hls"),
    data_vd_airplay: el.getAttribute("data-vd-airplay"),
  }));
}

function snapshot(video: HTMLVideoElement | null, session: PlaybackSession | null, engine: PlaybackEngine | null, originMs: number) {
  const apple = video as AppleVideo | null;
  const trace = readAttachTrace(video);
  const owner = inferDiagnosticOwner(video, session, engine);
  const rows: Record<string, unknown> = {
    user_agent: typeof navigator !== "undefined" ? navigator.userAgent : "",
    platform: typeof navigator !== "undefined" ? navigator.platform : "",
    engine: owner,
    attach_engine: engine,
    engine_reason: trace.engineReason ?? null,
    hls_js_supported: trace.hlsJsSupported ?? null,
    session_id: session?.id,
    delivery: session?.delivery,
    hls_attach: session?.hls_attach,
    vod_ondemand: session?.vod_ondemand,
    vod_plan_kind: session?.vod_plan_kind,
    gen_start_seg: session?.gen_start_seg,
    generation_id: session?.generation_id,
    movie_duration_ms: session?.duration_ms,
    video_duration: video && Number.isFinite(video.duration) ? video.duration : String(video?.duration ?? ""),
    duration_clocks: durationClocks(video, session, trace),
    initial_hls_listed_duration_ms: trace.playlistDurationMs ?? null,
    playlist_type: trace.playlistType ?? null,
    seekable_from_ms: session?.seekable_from_ms,
    origin_ms: originMs,
    seekable_window: video ? ranges(video.seekable) : "",
    buffered: video ? ranges(video.buffered) : "",
    video_action: session?.decision?.video?.action,
    audio_action: session?.decision?.audio?.action,
    playback: session?.decision?.playback,
    src: video?.getAttribute("src") ?? "",
    currentSrc: video?.currentSrc,
    readyState: video?.readyState,
    networkState: video?.networkState,
    paused: video?.paused,
    ended: video?.ended,
    currentTime: video?.currentTime,
    webkitDisplayingFullscreen: apple?.webkitDisplayingFullscreen,
    webkitPresentationMode: apple?.webkitPresentationMode,
    webkitSupportsFullscreen: apple?.webkitSupportsFullscreen,
    disableRemotePlayback: video?.disableRemotePlayback,
    airplay_policy: trace.airplayPolicy ?? "none",
    airplay_alternate: Boolean(video?.querySelector("source[data-vd-airplay]")),
    managedMediaSource: typeof (globalThis as { ManagedMediaSource?: unknown }).ManagedMediaSource !== "undefined",
    mediaSource: typeof MediaSource !== "undefined",
    mms_available: trace.mmsAvailable ?? null,
    mse_available: trace.mseAvailable ?? null,
    source_children: video?.querySelectorAll("source").length ?? 0,
    source_types: video
      ? [...video.querySelectorAll("source")].map((el) => el.type).join(",")
      : "",
    source_detail: sourceDetails(video),
    fullscreen_trace: trace.fullscreenSnaps,
    logical_positions: trace.logicalPositions,
    currentTime_writes: trace.currentTimeWrites,
    playlist_snaps: trace.playlistSnaps,
    network_abort_summary: trace.abortSummary,
    last_user_control: trace.userControls[trace.userControls.length - 1] ?? null,
    viewdock_pause_calls: trace.viewdockPauses,
    pause_attributions: trace.pauseAttributions,
    hls_errors: trace.hlsErrors,
    fs_window_log: trace.windowEvents.slice(-80),
    attach_log: trace.events.slice(-40),
  };
  return rows;
}

function copyDump(text: string, pre: HTMLElement | null): boolean {
  try {
    if (pre) {
      const sel = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(pre);
      sel?.removeAllRanges();
      sel?.addRange(range);
      if (document.execCommand("copy")) {
        sel?.removeAllRanges();
        return true;
      }
      sel?.removeAllRanges();
    }
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.cssText = "position:fixed;top:0;left:0;width:2em;height:2em;opacity:0.01;border:none;padding:0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export function PlaybackDiagnostics({ video, session, engine, originMs }: Props) {
  const [rows, setRows] = useState(() => snapshot(video, session, engine, originMs));
  const [copied, setCopied] = useState<"ok" | "fail" | null>(null);
  const preRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const tick = () => {
      if (video) noteDisplayingFsChange(video, session?.id);
      setRows(snapshot(video, session, engine, originMs));
    };
    tick();
    const id = window.setInterval(tick, 500);
    return () => window.clearInterval(id);
  }, [video, session, engine, originMs]);

  useEffect(() => {
    if (!video) return;
    const mark = (ev: string) => noteMedia(video, ev, session?.id);
    const onBegin = () => mark("webkitbeginfullscreen");
    const onEnd = () => mark("webkitendfullscreen");
    const onMode = () => mark("webkitpresentationmodechanged");
    video.addEventListener("webkitbeginfullscreen", onBegin);
    video.addEventListener("webkitendfullscreen", onEnd);
    video.addEventListener("webkitpresentationmodechanged", onMode);
    const mo = new MutationObserver(() => noteAttach(video, "source_children_changed", `count=${video.querySelectorAll("source").length}`));
    mo.observe(video, { childList: true, subtree: true, attributes: true, attributeFilter: ["src", "type"] });
    return () => {
      video.removeEventListener("webkitbeginfullscreen", onBegin);
      video.removeEventListener("webkitendfullscreen", onEnd);
      video.removeEventListener("webkitpresentationmodechanged", onMode);
      mo.disconnect();
    };
  }, [video, session?.id]);

  const text = JSON.stringify(rows, null, 2);

  return (
    <div
      className="pointer-events-auto absolute right-2 top-14 z-30 max-h-[70vh] w-[min(100%,22rem)] overflow-auto rounded-md bg-black/80 p-2 text-[10px] leading-snug text-white/90"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="font-medium">vd_debug</span>
        <button
          type="button"
          className="tap min-h-8 min-w-[4.5rem] rounded border border-white/30 px-2 py-1"
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            const ok = copyDump(text, preRef.current);
            if (!ok && window.isSecureContext && navigator.clipboard?.writeText) {
              void navigator.clipboard.writeText(text).then(
                () => {
                  setCopied("ok");
                  window.setTimeout(() => setCopied(null), 1500);
                },
                () => setCopied("fail"),
              );
              return;
            }
            setCopied(ok ? "ok" : "fail");
            window.setTimeout(() => setCopied(null), 1500);
          }}
        >
          {copied === "ok" ? "copied" : copied === "fail" ? "failed" : "copy"}
        </button>
      </div>
      <pre ref={preRef} className="whitespace-pre-wrap break-all">{text}</pre>
    </div>
  );
}
