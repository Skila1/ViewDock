import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";

export function UsersPage() {
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [roleIds, setRoleIds] = useState<string[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const detail = useQuery({
    queryKey: ["user", selected],
    queryFn: () => api.getUser(selected!),
    enabled: Boolean(selected),
  });

  const onCreate = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      await api.createUser({
        username,
        password,
        display_name: displayName,
        role_ids: roleIds,
      });
      setUsername("");
      setPassword("");
      setDisplayName("");
      setRoleIds([]);
      await qc.invalidateQueries({ queryKey: ["users"] });
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "create failed");
    }
  };

  const toggleRole = (id: string, current: string[], set: (v: string[]) => void) => {
    set(current.includes(id) ? current.filter((x) => x !== id) : [...current, id]);
  };

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <div>
        <h1 className="mb-3 text-base font-medium">Users</h1>
        <ul className="divide-y divide-line rounded-md border border-line">
          {(users.data ?? []).map((u) => (
            <li key={u.id}>
              <button
                type="button"
                className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-overlay"
                onClick={() => setSelected(u.id)}
              >
                <span>
                  {u.display_name || u.username}
                  <span className="ml-2 text-xs text-dim">{u.username}</span>
                  {u.disabled ? <span className="ml-2 text-[10px] text-danger">disabled</span> : null}
                </span>
                <span className="text-[10px] text-dim">{(u.roles ?? []).join(", ") || (u.is_admin ? "admin" : "")}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>

      <div className="space-y-6">
        <form onSubmit={onCreate} className="space-y-2">
          <h2 className="text-sm font-medium">Create user</h2>
          <input className="w-full" placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          <input className="w-full" placeholder="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          <input className="w-full" type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          <div className="flex flex-wrap gap-2 text-xs">
            {(roles.data ?? []).map((r) => (
              <label key={r.id} className="flex items-center gap-1">
                <input
                  type="checkbox"
                  checked={roleIds.includes(r.id)}
                  onChange={() => toggleRole(r.id, roleIds, setRoleIds)}
                />
                {r.name}
              </label>
            ))}
          </div>
          {err ? <p className="text-xs text-danger">{err}</p> : null}
          <button type="submit" className="rounded-md bg-accent px-3 py-1.5 text-sm text-black">
            Create
          </button>
        </form>

        {detail.data ? (
          <div className="space-y-3 rounded-md border border-line p-3">
            <h2 className="text-sm font-medium">{detail.data.display_name || detail.data.username}</h2>
            <div className="flex flex-wrap gap-2 text-xs">
              {(roles.data ?? []).map((r) => (
                <label key={r.id} className="flex items-center gap-1">
                  <input
                    type="checkbox"
                    checked={(detail.data.role_ids ?? []).includes(r.id)}
                    onChange={async () => {
                      const next = (detail.data.role_ids ?? []).includes(r.id)
                        ? (detail.data.role_ids ?? []).filter((id) => id !== r.id)
                        : [...(detail.data.role_ids ?? []), r.id];
                      await api.patchUser(detail.data.id, { role_ids: next });
                      await qc.invalidateQueries({ queryKey: ["user", detail.data.id] });
                      await qc.invalidateQueries({ queryKey: ["users"] });
                    }}
                  />
                  {r.name}
                </label>
              ))}
            </div>
            <button
              type="button"
              className="text-xs text-danger"
              onClick={async () => {
                await api.patchUser(detail.data.id, { disabled: !detail.data.disabled });
                await qc.invalidateQueries({ queryKey: ["users"] });
                await qc.invalidateQueries({ queryKey: ["user", detail.data.id] });
              }}
            >
              {detail.data.disabled ? "Enable" : "Disable"}
            </button>
            <div>
              <h3 className="mb-1 text-xs font-medium">Library grants</h3>
              <ul className="space-y-1 text-xs">
                {(libs.data ?? []).map((lib) => {
                  const g = (detail.data.grants ?? []).find((x) => x.library_id === lib.id);
                  return (
                    <li key={lib.id} className="flex items-center justify-between gap-2">
                      <span>{lib.name}</span>
                      <span className="flex items-center gap-2">
                        <label className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={Boolean(g)}
                            onChange={async () => {
                              if (g) await api.deleteUserGrant(detail.data.id, lib.id);
                              else await api.setUserGrant(detail.data.id, { library_id: lib.id, can_download: false });
                              await qc.invalidateQueries({ queryKey: ["user", detail.data.id] });
                            }}
                          />
                          access
                        </label>
                        <label className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={Boolean(g?.can_download)}
                            disabled={!g}
                            onChange={async () => {
                              await api.setUserGrant(detail.data.id, {
                                library_id: lib.id,
                                can_download: !g?.can_download,
                              });
                              await qc.invalidateQueries({ queryKey: ["user", detail.data.id] });
                            }}
                          />
                          download
                        </label>
                      </span>
                    </li>
                  );
                })}
              </ul>
            </div>
          </div>
        ) : (
          <p className="text-xs text-dim">Select a user to edit groups and library grants.</p>
        )}
      </div>
    </div>
  );
}
