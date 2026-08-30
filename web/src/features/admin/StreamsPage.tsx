import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";

export function StreamsPage() {
  const q = useQuery({ queryKey: ["streams"], queryFn: api.adminStreams, refetchInterval: 5000 });
  const rows = q.data ?? [];

  return (
    <div>
      <h1 className="mb-3 text-base font-medium">Streams</h1>
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
                <td>{row.username || row.user || "—"}</td>
                <td>{row.item_title || row.title || "—"}</td>
                <td className="text-dim">{row.delivery || "—"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      </div>
      {rows.length === 0 ? <p className="mt-3 text-xs text-dim">No live sessions.</p> : null}
    </div>
  );
}
