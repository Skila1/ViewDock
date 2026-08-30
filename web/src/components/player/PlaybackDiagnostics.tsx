import { useEffect, useState } from "react";
import type { PlaybackEngine } from "@/playback/policy";
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

function snapshot(video: HTMLVideoElement | null, session: PlaybackSession | null, engine: PlaybackEngine | null, originMs: number) {
  const apple = video as AppleVideo | null;
  const rows: Record<string, string | number | boolean | null | undefined> = {
    user_agent: typeof navigator !== "undefined" ? navigator.userAgent : "",
    platform: typeof navigator !== "undefined" ? navigator.platform : "",
    engine,
    session_id: session?.id,
    delivery: session?.delivery,
    hls_attach: session?.hls_attach,
    duration_ms: session?.duration_ms,
    seekable_from_ms: session?.seekable_from_ms,
    origin_ms: originMs,
    video_action: session?.decision?.video?.action,
    audio_action: session?.decision?.audio?.action,
    playback: session?.decision?.playback,
    src: video?.getAttribute("src") ?? "",
    currentSrc: video?.currentSrc,
    readyState: video?.readyState,
    networkState: video?.networkState,
    paused: video?.paused,
    ended: video?.ended,
    duration: video && Number.isFinite(video.duration) ? video.duration : String(video?.duration),
    currentTime: video?.currentTime,
    seekable: video ? ranges(video.seekable) : "",
    buffered: video ? ranges(video.buffered) : "",
    webkitDisplayingFullscreen: apple?.webkitDisplayingFullscreen,
    webkitPresentationMode: apple?.webkitPresentationMode,
    webkitSupportsFullscreen: apple?.webkitSupportsFullscreen,
    disableRemotePlayback: video?.disableRemotePlayback,
    managedMediaSource: typeof (globalThis as { ManagedMediaSource?: unknown }).ManagedMediaSource !== "undefined",
    mediaSource: typeof MediaSource !== "undefined",
    source_children: video?.querySelectorAll("source").length ?? 0,
    source_types: video
      ? [...video.querySelectorAll("source")].map((el) => el.type).join(",")
      : "",
    airplay_alternate: Boolean(video?.querySelector("source[data-vd-airplay]")),
  };
  return rows;
}

export function PlaybackDiagnostics({ video, session, engine, originMs }: Props) {
  const [rows, setRows] = useState(() => snapshot(video, session, engine, originMs));
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const tick = () => setRows(snapshot(video, session, engine, originMs));
    tick();
    const id = window.setInterval(tick, 500);
    return () => window.clearInterval(id);
  }, [video, session, engine, originMs]);

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
          className="rounded border border-white/30 px-1.5 py-0.5"
          onClick={() => {
            void navigator.clipboard.writeText(text).then(() => {
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1500);
            });
          }}
        >
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className="whitespace-pre-wrap break-all">{text}</pre>
    </div>
  );
}
