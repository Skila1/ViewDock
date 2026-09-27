import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { RoleRow } from "@/types/api.gen";

export function RolesPage() {
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const perms = useQuery({ queryKey: ["permissions"], queryFn: api.listPermissions });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [selected, setSelected] = useState<RoleRow | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [err, setErr] = useState("");

  const open = (r: RoleRow) => {
    setSelected(r);
    setDesc(r.description ?? "");
    setPicked(r.permissions ?? []);
  };

  const onCreate = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      await api.createRole({ name, description: desc, permissions: ["media.upload", "shares.create"] });
      setName("");
      setDesc("");
      await qc.invalidateQueries({ queryKey: ["roles"] });
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "create failed");
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-base font-medium">Groups</h1>
        <p className="text-sm text-dim">
          Permissions are the source of truth. Library access is granted to a user or a group.
        </p>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {(roles.data ?? []).map((r) => (
          <button
            key={r.id}
            type="button"
            className="rounded-md border border-line p-4 text-left hover:border-accent/40"
            onClick={() => open(r)}
          >
            <div className="flex items-center justify-between">
              <h3 className="font-medium">{r.name}</h3>
              <span className="text-[10px] text-dim">{r.is_system ? "Built-in" : "Custom"}</span>
            </div>
            <p className="mt-1 text-xs text-dim">{r.description || "No description"}</p>
            <p className="mt-2 text-[11px] text-dim">
              {r.member_count ?? 0} members · {(r.permissions ?? []).length} permissions
            </p>
          </button>
        ))}
      </div>

      <form onSubmit={onCreate} className="max-w-md space-y-2">
        <h2 className="text-sm font-medium">Create group</h2>
        <input className="w-full" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} required />
        <input className="w-full" placeholder="Description" value={desc} onChange={(e) => setDesc(e.target.value)} />
        {err ? <p className="text-xs text-danger">{err}</p> : null}
        <button type="submit" className="rounded-md bg-accent px-3 py-1.5 text-sm text-white">
          Create
        </button>
      </form>

      {selected ? (
        <form
          className="max-w-lg space-y-3 rounded-md border border-line p-4"
          onSubmit={async (e) => {
            e.preventDefault();
            await api.patchRole(selected.id, {
              description: desc,
              permissions: selected.is_system ? undefined : picked,
            });
            setSelected(null);
            await qc.invalidateQueries({ queryKey: ["roles"] });
          }}
        >
          <h2 className="text-sm font-medium">{selected.name}</h2>
          <input className="w-full" value={desc} onChange={(e) => setDesc(e.target.value)} />
          {selected.is_system ? (
            <p className="text-xs text-dim">Built-in group permissions cannot be changed.</p>
          ) : (
            <div className="grid gap-1 text-xs">
              {(perms.data ?? [])
                .filter((p) => p.name !== "admin")
                .map((p) => (
                  <label key={p.id} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={picked.includes(p.name)}
                      onChange={() =>
                        setPicked((cur) =>
                          cur.includes(p.name) ? cur.filter((x) => x !== p.name) : [...cur, p.name],
                        )
                      }
                    />
                    <span>
                      {p.name}
                      <span className="ml-2 text-dim">{p.description}</span>
                    </span>
                  </label>
                ))}
            </div>
          )}
          <div className="text-xs">
            <p className="mb-1 text-dim">Add member</p>
            <select
              className="w-full"
              defaultValue=""
              onChange={async (e) => {
                const id = e.target.value;
                if (!id) return;
                await api.addRoleMembers(selected.id, [id]);
                await qc.invalidateQueries({ queryKey: ["roles"] });
                e.target.value = "";
              }}
            >
              <option value="">Select user…</option>
              {(users.data ?? []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.display_name || u.username}
                </option>
              ))}
            </select>
          </div>
          <div className="flex gap-2">
            <button type="submit" className="rounded-md bg-accent px-3 py-1.5 text-sm text-white">
              Save
            </button>
            {!selected.is_system ? (
              <button
                type="button"
                className="text-xs text-danger"
                onClick={async () => {
                  await api.deleteRole(selected.id);
                  setSelected(null);
                  await qc.invalidateQueries({ queryKey: ["roles"] });
                }}
              >
                Delete
              </button>
            ) : null}
            <button type="button" className="text-xs text-dim" onClick={() => setSelected(null)}>
              Close
            </button>
          </div>
        </form>
      ) : null}
    </div>
  );
}
