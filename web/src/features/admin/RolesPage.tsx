import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { RoleRow } from "@/types/api.gen";
import { cn } from "@/lib/cn";
import { AdminModal, Card, CardGrid, errText, NoteLine, PageHeader, primaryBtn, secondaryBtn, type Note } from "./ui";

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

/** Ready-made permission sets for common jobs; the admin can adjust them before creating. */
const GROUP_TEMPLATES: { name: string; description: string; permissions: string[] }[] = [
  { name: "Viewer", description: "Watch the libraries the group is granted; nothing else", permissions: [] },
  { name: "Uploader", description: "Viewer who can also upload and share", permissions: ["media.upload", "shares.create"] },
  { name: "Library manager", description: "Organise libraries: upload, delete, fix metadata", permissions: ["libraries.manage", "media.upload", "media.delete", "shares.create"] },
  { name: "Moderator", description: "Look after people: accounts, shares, live streams and logs", permissions: ["users.manage", "shares.manage", "streams.inspect", "logs.read"] },
];

/** Groups by rank: the most permissions first, so Superadmin sits above Administrator and User last. */
export function byRank(a: RoleRow, b: RoleRow): number {
  const rank = (r: RoleRow) => (r.id === "sys-superadmin" ? 1000 : 0) + (r.permissions ?? []).length;
  return rank(b) - rank(a) || a.name.localeCompare(b.name);
}

function CreateGroupModal({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const qc = useQueryClient();
  const perms = useQuery({ queryKey: ["permissions"], queryFn: api.listPermissions });
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [picked, setPicked] = useState<string[]>(["media.upload", "shares.create"]);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const available = (perms.data ?? []).filter((p) => p.name !== "admin" && p.name !== "superadmin");
  const known = new Set(available.map((p) => p.name));

  const create = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    setNote(null);
    try {
      const role = await api.createRole({ name: name.trim(), description: desc.trim(), permissions: picked });
      await qc.invalidateQueries({ queryKey: ["roles"] });
      onCreated(role.id);
      onClose();
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not create the group") });
      setBusy(false);
    }
  };

  return (
    <AdminModal
      title="Create group"
      description="Pick a template or choose permissions yourself. Library access for the group is set under Library access."
      onClose={onClose}
      footer={
        <>
          <button type="button" className={secondaryBtn} onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={primaryBtn} disabled={busy || !name.trim()} onClick={() => void create()}>
            Create
          </button>
        </>
      }
    >
      <div className="space-y-2">
        <p className="text-xs font-medium">Templates</p>
        <div className="grid gap-2 sm:grid-cols-2">
          {GROUP_TEMPLATES.map((t) => (
            <button
              key={t.name}
              type="button"
              className="rounded-md border border-line p-2 text-left hover:border-accent/50"
              onClick={() => {
                setName((n) => n || t.name);
                setDesc(t.description);
                setPicked(t.permissions.filter((p) => known.size === 0 || known.has(p)));
              }}
            >
              <span className="text-sm">{t.name}</span>
              <span className="mt-0.5 block text-[11px] text-dim">{t.description}</span>
            </button>
          ))}
        </div>
      </div>
      <form onSubmit={create} className="space-y-2">
        <label className="block text-xs text-dim">
          Name
          <input className="mt-1 w-full" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </label>
        <label className="block text-xs text-dim">
          Description
          <input className="mt-1 w-full" value={desc} onChange={(e) => setDesc(e.target.value)} />
        </label>
        <fieldset className="grid gap-1 text-xs">
          <legend className="mb-1 text-dim">Permissions ({picked.length})</legend>
          {perms.isLoading ? <p className="text-dim">Loading…</p> : null}
          {available.map((p) => (
            <label key={p.id} className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
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
      </form>
      <NoteLine note={note} />
    </AdminModal>
  );
}

export function RolesPage() {
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const sorted = [...(roles.data ?? [])].sort(byRank);
  const selected = sorted.find((r) => r.id === selectedId) ?? null;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Groups"
        description="Permissions and membership are managed here. Library access for a group is set under Library access."
        actions={
          <button type="button" className={primaryBtn} onClick={() => setCreating(true)}>
            Create group
          </button>
        }
      />
      <CardGrid>
        <Card id="groups-list" title="All groups" description="Highest rank first.">
          {roles.isLoading ? <p className="text-xs text-dim">Loading groups…</p> : null}
          {roles.isError ? <p className="text-xs text-danger">{errText(roles.error, "Groups could not be loaded")}</p> : null}
          <div className="grid gap-2 sm:grid-cols-2">
            {sorted.map((r) => (
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
                  {r.member_count ?? 0} member{r.member_count === 1 ? "" : "s"} · {(r.permissions ?? []).length} permissions
                </p>
              </button>
            ))}
          </div>
        </Card>
        {selected ? (
          <GroupEditor key={selected.id} role={selected} onClose={() => setSelectedId(null)} />
        ) : (
          <p className="text-xs text-dim">Select a group to edit its permissions and members.</p>
        )}
      </CardGrid>
      {creating ? <CreateGroupModal onClose={() => setCreating(false)} onCreated={setSelectedId} /> : null}
    </div>
  );
}
