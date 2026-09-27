import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { RoleRow } from "@/types/api.gen";
import { cn } from "@/lib/cn";
import { Card, CardGrid, errText, NoteLine, PageHeader, primaryBtn, secondaryBtn, type Note } from "./ui";

function GroupEditor({ role, onClose }: { role: RoleRow; onClose: () => void }) {
  const qc = useQueryClient();
  const perms = useQuery({ queryKey: ["permissions"], queryFn: api.listPermissions });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const detail = useQuery({ queryKey: ["role", role.id], queryFn: () => api.getRole(role.id) });
  const [desc, setDesc] = useState(role.description ?? "");
  const [picked, setPicked] = useState<string[]>(role.permissions ?? []);
  const [note, setNote] = useState<Note>(null);
  const members = detail.data?.members ?? [];
  const memberIds = new Set(members.map((m) => m.id));

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["roles"] });
    await qc.invalidateQueries({ queryKey: ["role", role.id] });
    await qc.invalidateQueries({ queryKey: ["users"] });
  };
  const run = async (fn: () => Promise<unknown>, ok: string, fallback: string) => {
    setNote(null);
    try {
      await fn();
      setNote({ ok: true, text: ok });
      await refresh();
    } catch (e) {
      setNote({ ok: false, text: errText(e, fallback) });
    }
  };

  return (
    <Card
      id="group-editor"
      title={role.name}
      description={role.is_system ? "Built-in group. Its permissions cannot be changed." : "Custom group."}
      aside={
        <button type="button" className="text-xs text-dim" onClick={onClose}>
          Close
        </button>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void run(
            () => api.patchRole(role.id, { description: desc, permissions: role.is_system ? undefined : picked }),
            "Group saved",
            "Could not save the group",
          );
        }}
      >
        <label className="block text-xs text-dim">
          Description
          <input className="mt-1 w-full" value={desc} onChange={(e) => setDesc(e.target.value)} />
        </label>
        {role.is_system ? null : (
          <fieldset className="grid gap-1 text-xs">
            <legend className="mb-1 text-dim">Permissions</legend>
            {(perms.data ?? [])
              .filter((p) => p.name !== "admin")
              .map((p) => (
                <label key={p.id} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={picked.includes(p.name)}
                    onChange={() => setPicked((cur) => (cur.includes(p.name) ? cur.filter((x) => x !== p.name) : [...cur, p.name]))}
                  />
                  <span>
                    {p.name}
                    <span className="ml-2 text-dim">{p.description}</span>
                  </span>
                </label>
              ))}
          </fieldset>
        )}
        <div className="flex flex-wrap gap-2">
          <button type="submit" className={primaryBtn}>
            Save
          </button>
          {!role.is_system ? (
            <button
              type="button"
              className="text-xs text-danger"
              onClick={() => {
                if (!window.confirm(`Delete the ${role.name} group?`)) return;
                void run(() => api.deleteRole(role.id), "Group deleted", "Could not delete the group").then(onClose);
              }}
            >
              Delete group
            </button>
          ) : null}
        </div>
      </form>

      <div className="space-y-2 border-t border-line pt-3">
        <h3 className="text-xs font-medium">Members</h3>
        {detail.isLoading ? <p className="text-xs text-dim">Loading members…</p> : null}
        {detail.isSuccess && members.length === 0 ? <p className="text-xs text-dim">No members yet.</p> : null}
        {members.length ? (
          <ul className="divide-y divide-line rounded-md border border-line text-sm">
            {members.map((m) => (
              <li key={m.id} className="flex items-center justify-between gap-2 px-3 py-2">
                <span>
                  {m.display_name || m.username}
                  <span className="ml-2 text-xs text-dim">{m.username}</span>
                </span>
                <button
                  type="button"
                  className="text-xs text-danger"
                  onClick={() => void run(() => api.removeRoleMember(role.id, m.id), "Member removed", "Could not remove the member")}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        ) : null}
        <select
          className="w-full text-xs"
          value=""
          onChange={(e) => {
            const id = e.target.value;
            if (id) void run(() => api.addRoleMembers(role.id, [id]), "Member added", "Could not add the member");
          }}
        >
          <option value="">Add member…</option>
          {(users.data ?? [])
            .filter((u) => !memberIds.has(u.id))
            .map((u) => (
              <option key={u.id} value={u.id}>
                {u.display_name || u.username}
              </option>
            ))}
        </select>
      </div>
      <NoteLine note={note} />
    </Card>
  );
}

export function RolesPage() {
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [note, setNote] = useState<Note>(null);
  const selected = (roles.data ?? []).find((r) => r.id === selectedId) ?? null;

  const onCreate = async (e: FormEvent) => {
    e.preventDefault();
    setNote(null);
    try {
      const role = await api.createRole({ name, description: desc, permissions: ["media.upload", "shares.create"] });
      setName("");
      setDesc("");
      await qc.invalidateQueries({ queryKey: ["roles"] });
      setSelectedId(role.id);
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not create the group") });
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader
        title="Groups"
        description="Permissions and membership are managed here. Library access for a group is set under Library access."
      />
      <CardGrid>
        <div className="min-w-0 space-y-4">
          <Card id="groups-list" title="All groups">
            {roles.isLoading ? <p className="text-xs text-dim">Loading groups…</p> : null}
            {roles.isError ? <p className="text-xs text-danger">{errText(roles.error, "Groups could not be loaded")}</p> : null}
            <div className="grid gap-2 sm:grid-cols-2">
              {(roles.data ?? []).map((r) => (
                <button
                  key={r.id}
                  type="button"
                  className={cn("rounded-md border border-line p-3 text-left hover:border-accent/40", r.id === selectedId && "border-accent/60")}
                  onClick={() => setSelectedId(r.id)}
                >
                  <div className="flex items-center justify-between gap-2">
                    <h3 className="text-sm font-medium">{r.name}</h3>
                    <span className="text-[10px] text-dim">{r.is_system ? "Built-in" : "Custom"}</span>
                  </div>
                  <p className="mt-1 text-xs text-dim">{r.description || "No description"}</p>
                  <p className="mt-2 text-[11px] text-dim">
                    {r.member_count ?? 0} members · {(r.permissions ?? []).length} permissions
                  </p>
                </button>
              ))}
            </div>
          </Card>
          <Card id="groups-create" title="Create group" description="New groups start with upload and share permissions.">
            <form onSubmit={onCreate} className="space-y-2">
              <input className="w-full" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} required />
              <input className="w-full" placeholder="Description" value={desc} onChange={(e) => setDesc(e.target.value)} />
              <NoteLine note={note} />
              <button type="submit" className={secondaryBtn}>
                Create
              </button>
            </form>
          </Card>
        </div>
        {selected ? (
          <GroupEditor key={selected.id} role={selected} onClose={() => setSelectedId(null)} />
        ) : (
          <p className="text-xs text-dim">Select a group to edit its permissions and members.</p>
        )}
      </CardGrid>
    </div>
  );
}
