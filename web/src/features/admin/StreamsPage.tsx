import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { formatClock } from "@/lib/format";
import type { StreamRow } from "@/types/api.gen";
import { Card, PageHeader } from "./ui";

function playbackLabel(row: StreamRow): string {
  if (row.delivery === "direct") return "Direct play";
  if (row.video_codec) return `${row.video_copy ? "Original" : "Transcode"} ${row.video_codec.toUpperCase()}`;
  return row.playback === "direct" ? "Direct stream" : row.playback || row.delivery || "n/a";
}

/** Seconds of video the player has ready: the buffer cache when used, else its own buffer. */
function bufferHealth(row: StreamRow): { text: string; tone: string } {
  const c = row.client;
  if (!c) return { text: "n/a", tone: "text-dim" };
  const ahead = Math.max(c.cache_ahead_ms, c.buffer_ahead_ms) / 1000;
  const tone = c.paused ? "text-dim" : ahead >= 60 ? "text-ok" : ahead >= 15 ? "text-warn" : "text-danger";
  const parts = [`${formatClock(ahead * 1000)} ahead`];
  if (c.cache_ahead_ms) parts.push(`${(c.cache_bytes / 1e9).toFixed(1)} GB cached`);
  if (c.cache_hit_rate) parts.push(`${Math.round(c.cache_hit_rate * 100)}% hits`);
  if (c.paused) parts.push("paused");
  return { text: parts.join(" · "), tone };
}

export function StreamsPage() {
  const q = useQuery({ queryKey: ["streams"], queryFn: api.adminStreams, refetchInterval: 5000 });
  const rows = q.data ?? [];

  return (
    <div className="space-y-4">
      <PageHeader
        title="Streams"
        description={
          <>
            Live playback sessions. Timelines of ended sessions stay available under{" "}
            <Link to="/admin/diagnostics" className="text-accent">
              Resilience
            </Link>
            .
          </>
        }
      />
      <Card id="streams-live" title="Live sessions" description={q.isSuccess ? `${rows.length} active` : undefined}>
        <div className="h-scroll">
          <table className="w-full min-w-[900px] text-left text-sm">
            <thead className="text-xs text-dim">
              <tr>
                <th className="py-1 font-normal">Session</th>
                <th className="font-normal">Profile</th>
                <th className="font-normal">Title</th>
                <th className="font-normal">Source</th>
                <th className="font-normal">Playback</th>
                <th className="font-normal">Bitrate</th>
                <th className="font-normal">Position</th>
                <th className="font-normal">Buffer</th>
                <th className="font-normal">Network</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const id = row.session_id || row.id;
                return (
                  <tr key={id} className="border-t border-line">
                    <td className="py-2">
                      <Link to={`/admin/streams/${id}`} className="text-accent">
                        {id.slice(0, 8)}
                      </Link>
                    </td>
                    <td>{row.username || row.user || (row.guest ? "Guest" : "n/a")}</td>
                    <td className="max-w-[14rem] truncate">{row.item_title || row.title || "n/a"}</td>
                    <td className="text-dim">{row.source ? row.source.split(":")[0] : "local"}</td>
                    <td className="text-dim">{playbackLabel(row)}</td>
                    <td className="text-dim">{row.bitrate_bps ? `${(row.bitrate_bps / 1e6).toFixed(1)} Mbps` : "n/a"}</td>
                    <td className="text-dim tabular-nums">
                      {formatClock(row.position_ms ?? 0)}
                      {row.duration_ms ? ` / ${formatClock(row.duration_ms)}` : ""}
                    </td>
                    <td className={bufferHealth(row).tone}>{bufferHealth(row).text}</td>
                    <td className="text-dim">{row.client?.throughput_bps ? `${(row.client.throughput_bps / 1e6).toFixed(0)} Mbps` : "n/a"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {q.isLoading ? <p className="text-xs text-dim">Loading sessions…</p> : null}
        {q.isError ? <p className="text-xs text-danger">Live sessions could not be loaded.</p> : null}
        {q.isSuccess && rows.length === 0 ? <p className="text-xs text-dim">No live sessions.</p> : null}
      </Card>
    </div>
  );
}
