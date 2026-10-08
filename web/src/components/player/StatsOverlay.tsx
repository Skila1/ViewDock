import { X } from "lucide-react";
import type { PlaybackSession } from "@/types/api.gen";
import type { CacheStats } from "./bufferCache";

type Props = {
  video: HTMLVideoElement | null;
  session: PlaybackSession | null;
  engine: string;
  stats: CacheStats | null;
  onClose: () => void;
};

const mbps = (bps?: number) => (bps && bps > 0 ? `${(bps / 1e6).toFixed(1)} Mbps` : "n/a");
const clock = (sec: number) => {
  const s = Math.max(0, Math.round(sec));
  return `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
};
const size = (bytes: number) => (bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.round(bytes / 1e6)} MB`);

/** How the video reaches the player, in one line: "Direct Play · HEVC · 4K". */
export function deliveryLine(session: PlaybackSession | null, video: HTMLVideoElement | null): string {
  if (!session) return "Starting";
  const remote = session.remote_video;
  const local = session.decision?.video as { codec?: string; action?: string } | undefined;
  const codec = (remote?.codec || local?.codec || "").toUpperCase();
  let mode: string;
  if (session.delivery === "direct") mode = "Direct Play";
  else if (remote) mode = remote.copy ? "Direct Stream (original video)" : "Transcode";
  else mode = local?.action === "copy" ? "Direct Stream" : local?.action ? "Transcode" : "HLS";
  const h = video?.videoHeight ?? 0;
  const res = h >= 2000 ? "4K" : h >= 1000 ? "1080p" : h >= 700 ? "720p" : h ? `${h}p` : "";
  return [mode, codec, res].filter(Boolean).join(" · ");
}

/**
 * Stats for nerds: delivery, bitrates, the buffer cache and the decoder.
 * Toggled with I or from the settings menu.
 */
export function StatsOverlay({ video, session, engine, stats, onClose }: Props) {
  const q = video?.getVideoPlaybackQuality?.();
  const mseAhead = (() => {
    if (!video || !video.buffered.length) return 0;
    for (let i = 0; i < video.buffered.length; i++) {
      if (video.buffered.start(i) <= video.currentTime + 0.5 && video.buffered.end(i) >= video.currentTime) return video.buffered.end(i) - video.currentTime;
    }
    return 0;
  })();
  const audio = (session?.decision?.audio as { codec?: string } | undefined)?.codec;
  const rows: [string, string][] = [
    ["Playback", `${deliveryLine(session, video)}${session?.bitrate ? ` · ${mbps(session.bitrate)}` : ""}`],
    ["Resolution", video?.videoWidth ? `${video.videoWidth}x${video.videoHeight}` : "n/a"],
    ["Audio", audio ? audio.toUpperCase() : session?.remote_video ? "AAC (converted)" : "n/a"],
    ["Engine", `${engine}${session?.source ? ` · ${session.source.split(":")[0]}` : ""}`],
    ["Buffer (player)", `${clock(mseAhead)} ahead`],
  ];
  if (stats) {
    rows.push(
      ["Network", `${mbps(stats.throughputBps)}${stats.rate ? ` · ${stats.rate.toFixed(1)}x real time` : ""}`],
      ["Video bitrate", mbps(stats.mediaBitrateBps)],
      ["Cache", `${clock(stats.aheadSec)} ahead / ${clock(stats.behindSec)} behind (target ${clock(stats.window.ahead)} / ${clock(stats.window.behind)})`],
      ["Cache size", `${size(stats.bytes)} of ${size(stats.budget)}${stats.hitRate != null ? ` · ${Math.round(stats.hitRate * 100)}% hit rate` : ""}`],
    );
  } else {
    rows.push(["Cache", "Not used for this stream"]);
  }
  rows.push(["Frames", q ? `${q.droppedVideoFrames} dropped of ${q.totalVideoFrames}` : "n/a"]);

  return (
    <div
      className="pointer-events-auto absolute top-16 left-3 z-20 max-w-[calc(100%-1.5rem)] rounded-lg bg-black/80 p-3 font-mono text-[11px] leading-5 text-white shadow-xl ring-1 ring-white/10 sm:left-6"
      role="dialog"
      aria-label="Playback stats"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="mb-1 flex items-center justify-between gap-4">
        <span className="font-sans text-xs font-semibold">Stats for nerds</span>
        <button type="button" aria-label="Close stats" onClick={onClose} className="text-white/70 hover:text-white">
          <X size={14} />
        </button>
      </div>
      <table>
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k}>
              <td className="pr-3 align-top text-white/60">{k}</td>
              <td className="break-words">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
