import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { formatBytes } from "@/lib/format";
import { hasPerm } from "@/lib/perms";
import { useAuth } from "@/store/auth";
import { pruneCutoff } from "./logPrune";
import { Card, errText, NoteLine, PageHeader, secondaryBtn, type Note } from "./ui";

const dangerBtn = "rounded-full border border-danger/40 bg-danger/10 px-4 py-1.5 text-sm text-danger disabled:opacity-50";

function day(iso?: string) {
  return iso ? new Date(iso).toLocaleString() : "none";
}

/** How much the log holds, and pruning it by date or entirely. */
function ManageLogs() {
  const qc = useQueryClient();
  const { me } = useAuth();
  const canPrune = hasPerm(me, "settings.manage");
  const stats = useQuery({ queryKey: ["admin-log-stats"], queryFn: api.logStats, refetchInterval: 30000 });
  const [upTo, setUpTo] = useState("");
  const [note, setNote] = useState<Note>(null);
  const prune = useMutation({
    mutationFn: (before?: string) => api.pruneLogs(before),
    onSuccess: (r) => {
      setNote({ ok: true, text: `Deleted ${r.deleted.toLocaleString()} log ${r.deleted === 1 ? "entry" : "entries"}.` });
      setUpTo("");
      void qc.invalidateQueries({ queryKey: ["admin-log-stats"] });
      void qc.invalidateQueries({ queryKey: ["admin-logs"] });
    },
    onError: (e) => setNote({ ok: false, text: errText(e, "The logs could not be deleted.") }),
  });
  const st = stats.data;
  const cutoff = pruneCutoff(upTo);
  const pruneUpTo = () => {
    if (!cutoff) return;
    if (!window.confirm(`Delete every log entry up to and including ${new Date(upTo + "T00:00").toLocaleDateString()}? This cannot be undone.`)) return;
    prune.mutate(cutoff);
  };
  const pruneAll = () => {
    if (!window.confirm("Delete every log entry? This cannot be undone.")) return;
    prune.mutate(undefined);
  };
  return (
    <Card
      id="logs-manage"
      title="Manage logs"
      description={
        st?.retention_days
          ? `Entries older than ${st.retention_days} days are deleted automatically (Settings, Operations).`
          : "Logs are kept until you delete them here. Settings, Operations can delete old entries automatically instead."
      }
    >
      {stats.isError ? <p className="text-xs text-danger">Log size could not be loaded.</p> : null}
      {st ? (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-4">
          <div>
            <dt className="text-xs text-dim">Entries</dt>
            <dd className="tabular-nums">{st.rows.toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-dim">Size</dt>
            <dd className="tabular-nums">{formatBytes(st.bytes)}</dd>
          </div>
          <div>
            <dt className="text-xs text-dim">Oldest</dt>
            <dd>{day(st.oldest)}</dd>
          </div>
          <div>
            <dt className="text-xs text-dim">Newest</dt>
            <dd>{day(st.newest)}</dd>
          </div>
        </dl>
      ) : null}
      {canPrune ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="text-xs text-dim">
            Delete everything up to and including
            <input type="date" className="mt-1 block text-sm" value={upTo} max={new Date(Date.now() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 10)} onChange={(e) => setUpTo(e.target.value)} />
          </label>
          <button type="button" className={secondaryBtn} disabled={!cutoff || prune.isPending} onClick={pruneUpTo}>
            Delete up to this day
          </button>
          <button type="button" className={dangerBtn} disabled={prune.isPending || !st?.rows} onClick={pruneAll}>
            Prune all
          </button>
        </div>
      ) : (
        <p className="text-xs text-dim">Deleting logs needs the settings.manage permission.</p>
      )}
      <NoteLine note={note} />
    </Card>
  );
}

export function LogsPage() {
  const [level, setLevel] = useState("");
  const [category, setCategory] = useState("");
  const [q, setQ] = useState("");
  const logs = useQuery({
    queryKey: ["admin-logs", level, category, q],
    queryFn: () => api.listLogs({ level, category, q, limit: 100 }),
    refetchInterval: 8000,
  });

  return (
    <div className="space-y-4">
      <PageHeader
        title="Logs"
        description={
          <>
            Application, playback and browser journey events. Filter category <code>journey</code> to follow a visit from landing through
            sign-in to playback. Secrets are redacted. The same data is at GET /api/v1/admin/logs with a <code>logs.read</code> API key.
          </>
        }
      />
      <ManageLogs />
      <Card
        id="logs-events"
        title="Recent events"
        aside={
          <div className="flex flex-wrap gap-2">
            <select className="text-sm" value={level} onChange={(e) => setLevel(e.target.value)} aria-label="Level">
              <option value="">All levels</option>
              <option value="debug">debug</option>
              <option value="info">info</option>
              <option value="warn">warn</option>
              <option value="error">error</option>
            </select>
            <input
              className="text-sm"
              placeholder="category (journey, playback, app)"
              aria-label="Category"
              value={category}
              onChange={(e) => setCategory(e.target.value)}
            />
            <input className="text-sm" placeholder="search" aria-label="Search" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        }
      >
        {logs.isError ? <p className="text-xs text-danger">Logs could not be loaded.</p> : null}
        {logs.data?.items?.length ? (
          <ul className="divide-y divide-line rounded-md border border-line font-mono text-[12px]">
            {logs.data.items.map((row) => (
              <li key={row.id} className="px-3 py-2">
                <p className="text-dim">
                  {row.created_at} · {row.level} · {row.category}
                </p>
                <p className={row.level === "error" || row.level === "warn" ? "text-danger" : ""}>{row.message}</p>
                {row.details && Object.keys(row.details).length ? (
                  <pre className="mt-1 whitespace-pre-wrap text-dim">{JSON.stringify(row.details)}</pre>
                ) : null}
              </li>
            ))}
          </ul>
        ) : null}
        {logs.data?.items?.length === 0 ? <p className="text-xs text-dim">No log rows yet.</p> : null}
      </Card>
    </div>
  );
}
