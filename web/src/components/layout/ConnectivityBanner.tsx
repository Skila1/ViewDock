import { useEffect, useState } from "react";
import { CloudOff, RefreshCw } from "lucide-react";
import { getConnectivity, onConnectivity, replayOfflineMutations, type Connectivity } from "@/api/client";
import { listQueuedMutations, onQueueChange, resolveConflict, type QueuedMutation } from "@/lib/offlineQueue";

function age(ms: number): string {
  const minutes = Math.round((Date.now() - ms) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}

function describe(m: QueuedMutation): string {
  if (m.path.includes("/progress")) return "Watch progress";
  if (m.path.includes("/preferences")) return "Preferences";
  if (m.path.startsWith("/api/v1/me")) return "Profile";
  return `${m.method} ${m.path.replace("/api/v1/", "")}`;
}

// ConnectivityBanner shows degraded operation, queued changes and replay
// conflicts that need a user decision.
export function ConnectivityBanner() {
  const [conn, setConn] = useState<Connectivity>(getConnectivity());
  const [queue, setQueue] = useState<QueuedMutation[]>([]);

  useEffect(() => onConnectivity(setConn), []);
  useEffect(() => {
    let alive = true;
    const load = () => {
      listQueuedMutations()
        .then((rows) => alive && setQueue(rows))
        .catch(() => alive && setQueue([]));
    };
    load();
    const off = onQueueChange(load);
    return () => {
      alive = false;
      off();
    };
  }, []);

  const conflicts = queue.filter((m) => m.status === "conflict");
  const pending = queue.length - conflicts.length;
  if (conn.api && conflicts.length === 0 && pending === 0) return null;

  return (
    <div role="status" aria-live="polite" className="border-b border-line bg-raised px-4 py-2 text-xs md:px-8">
      {!conn.api ? (
        <p className="flex items-center gap-2 text-ink">
          <CloudOff className="h-4 w-4 shrink-0 text-warn" />
          <span>
            Server unreachable.
            {conn.staleSince !== undefined ? ` Showing data saved ${age(conn.staleSince)}.` : ""}
            {pending > 0 ? ` ${pending} change${pending === 1 ? "" : "s"} will sync when the connection returns.` : ""}
            {" "}Downloaded titles remain playable from Offline.
          </span>
        </p>
      ) : pending > 0 ? (
        <p className="flex items-center gap-2 text-dim">
          <RefreshCw className="h-4 w-4 shrink-0" />
          <span>
            {pending} queued change{pending === 1 ? "" : "s"} waiting to sync.
          </span>
          <button type="button" className="underline" onClick={() => void replayOfflineMutations()}>
            Sync now
          </button>
        </p>
      ) : null}
      {conflicts.map((m) => (
        <p key={m.id} className="mt-1 flex flex-wrap items-center gap-2 text-ink">
          <span>{describe(m)} changed on another device while you were offline.</span>
          <button
            type="button"
            className="underline"
            onClick={async () => {
              await resolveConflict(m.id, "keep");
              await replayOfflineMutations();
            }}
          >
            Keep mine
          </button>
          <button type="button" className="underline text-dim" onClick={() => void resolveConflict(m.id, "discard")}>
            Use server version
          </button>
        </p>
      ))}
    </div>
  );
}
