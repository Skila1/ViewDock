import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Card, PageHeader } from "./ui";

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
