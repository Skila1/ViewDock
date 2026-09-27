import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Card, PageHeader } from "./ui";

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
          <table className="w-full min-w-[520px] text-left text-sm">
            <thead className="text-xs text-dim">
              <tr>
                <th className="py-1 font-normal">Session</th>
                <th className="font-normal">User</th>
                <th className="font-normal">Title</th>
                <th className="font-normal">Delivery</th>
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
                    <td>{row.username || row.user || "n/a"}</td>
                    <td>{row.item_title || row.title || "n/a"}</td>
                    <td className="text-dim">{row.delivery || "n/a"}</td>
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
