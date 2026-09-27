import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";

export function GrantsPage() {
  const qc = useQueryClient();
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const [libId, setLibId] = useState("");
  const grants = useQuery({
    queryKey: ["lib-grants", libId],
    queryFn: () => api.listLibraryGrants(libId),
    enabled: Boolean(libId),
  });

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-base font-medium">Library grants</h1>
        <p className="text-sm text-dim">
          Admins see every library. Everyone else needs a user grant or a group grant.
        </p>
      </div>
      <select className="max-w-sm" value={libId} onChange={(e) => setLibId(e.target.value)}>
        <option value="">Select a library…</option>
        {(libs.data ?? []).map((l) => (
          <option key={l.id} value={l.id}>
            {l.name}
          </option>
        ))}
      </select>
      {libId ? (
        <div className="grid gap-6 md:grid-cols-2">
          <div>
            <h2 className="mb-2 text-sm font-medium">Users</h2>
            <ul className="divide-y divide-line rounded-md border border-line text-sm">
              {(grants.data?.users ?? []).map((g) => (
                <li key={g.user_id} className="flex items-center justify-between px-3 py-2">
                  <span>
                    {g.display_name || g.username}
                    {g.can_download ? <span className="ml-2 text-[10px] text-accent">download</span> : null}
                  </span>
                  <button
                    type="button"
                    className="text-xs text-danger"
                    onClick={async () => {
                      await api.deleteLibraryGrant(libId, { user_id: g.user_id });
                      await qc.invalidateQueries({ queryKey: ["lib-grants", libId] });
                    }}
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
            <select
              className="mt-2 w-full"
              defaultValue=""
              onChange={async (e) => {
                if (!e.target.value) return;
                await api.setLibraryGrant(libId, { user_id: e.target.value, can_download: false });
                await qc.invalidateQueries({ queryKey: ["lib-grants", libId] });
                e.target.value = "";
              }}
            >
              <option value="">Add user…</option>
              {(users.data ?? []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.display_name || u.username}
                </option>
              ))}
            </select>
          </div>
          <div>
            <h2 className="mb-2 text-sm font-medium">Groups</h2>
            <ul className="divide-y divide-line rounded-md border border-line text-sm">
              {(grants.data?.roles ?? []).map((g) => (
                <li key={g.role_id} className="flex items-center justify-between px-3 py-2">
                  <span>
                    {g.name}
                    {g.can_download ? <span className="ml-2 text-[10px] text-accent">download</span> : null}
                  </span>
                  <button
                    type="button"
                    className="text-xs text-danger"
                    onClick={async () => {
                      await api.deleteLibraryGrant(libId, { role_id: g.role_id });
                      await qc.invalidateQueries({ queryKey: ["lib-grants", libId] });
                    }}
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
            <select
              className="mt-2 w-full"
              defaultValue=""
              onChange={async (e) => {
                if (!e.target.value) return;
                await api.setLibraryGrant(libId, { role_id: e.target.value, can_download: false });
                await qc.invalidateQueries({ queryKey: ["lib-grants", libId] });
                e.target.value = "";
              }}
            >
              <option value="">Add group…</option>
              {(roles.data ?? []).map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </div>
        </div>
      ) : null}
    </div>
  );
}
