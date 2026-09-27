import { useQuery } from "@tanstack/react-query";
import { diagnosticsApi, type FlightEvent } from "@/api/diagnostics";
import { cn } from "@/lib/cn";
import { describeEventData, eventLabel, eventSeverity } from "./format";

const SEVERITY_TONE = { problem: "text-danger", warning: "text-warn", info: "text-ink" } as const;

function offset(first: number, at: string): string {
  const t = Date.parse(at);
  if (Number.isNaN(t) || Number.isNaN(first)) return "";
  const s = (t - first) / 1000;
  return `+${s.toFixed(s < 10 ? 2 : 1)} s`;
}

export function errText(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

export function FlightEventList({ events, showSession = false }: { events: FlightEvent[]; showSession?: boolean }) {
  const first = events.length ? Date.parse(events[0].at) : NaN;
  return (
    <ol className="divide-y divide-line rounded-md border border-line text-xs">
      {events.map((e, i) => {
        const severity = eventSeverity(e);
        const detail = describeEventData(e.data);
        return (
          <li key={`${e.at}-${e.type}-${i}`} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2">
            <span className="w-24 shrink-0 font-mono text-dim" title={new Date(e.at).toLocaleString()}>
              {showSession ? new Date(e.at).toLocaleTimeString() : offset(first, e.at)}
            </span>
            <span className={cn("font-medium", SEVERITY_TONE[severity])}>{eventLabel(e.type)}</span>
            <span className="text-[10px] uppercase tracking-wide text-dim">{e.origin ?? "server"}{e.node ? ` · node ${e.node}` : ""}</span>
            {detail ? <span className="min-w-0 break-words text-dim">{detail}</span> : null}
            {showSession ? <span className="font-mono text-dim">session {e.session_id.slice(0, 8)}</span> : null}
            {e.request_id || e.correlation_id ? (
              <span className="ml-auto font-mono text-[10px] text-dim" title="Correlation identifiers">
                {e.correlation_id ? `corr ${e.correlation_id}` : ""}
                {e.correlation_id && e.request_id ? " · " : ""}
                {e.request_id ? `req ${e.request_id}` : ""}
              </span>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}

/** Timeline of one session from the in-memory flight recorder. */
export function FlightTimeline({ sessionId, refetchInterval = 10_000 }: { sessionId: string; refetchInterval?: number | false }) {
  const q = useQuery({
    queryKey: ["flight-timeline", sessionId],
    queryFn: () => diagnosticsApi.flightTimeline(sessionId),
    enabled: Boolean(sessionId),
    refetchInterval,
  });
  if (!sessionId) return null;
  if (q.isLoading) return <p className="text-sm text-dim">Loading timeline…</p>;
  if (q.isError) return <p className="text-sm text-danger">{errText(q.error, "The timeline could not be loaded.")}</p>;
  const events = q.data ?? [];
  if (!events.length) {
    return (
      <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">
        No events recorded for this session. Timelines are kept in memory on the server that ran the session and expire
        after the retention period.
      </p>
    );
  }
  return <FlightEventList events={events} />;
}
