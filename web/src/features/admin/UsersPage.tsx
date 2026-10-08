import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { ageLimitLabel, ageLimitOptions, contentRatings, type ContentRestriction } from "@/api/households";
import { cn } from "@/lib/cn";
import { useAuth } from "@/store/auth";
import type { RoleRow, UserRow } from "@/types/api.gen";
import { byRank } from "./RolesPage";
import { AdminModal, errText, NoteLine, PageHeader, primaryBtn, secondaryBtn, type Note } from "./ui";

/** How long an account counts as new after joining. */
const NEW_FOR_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * When the person joined: their account's creation, or, when Discord sign-in
 * replaces registration, when they connected Discord.
 */
export function joinedAt(u: UserRow, discordOnly: boolean): string | undefined {
  return discordOnly ? (u.discord_linked_at ?? u.created_at) : u.created_at;
}

export function isNewUser(u: UserRow, discordOnly: boolean, now = Date.now()): boolean {
  const at = Date.parse(joinedAt(u, discordOnly) ?? "");
  return Number.isFinite(at) && now - at < NEW_FOR_MS;
}

type Filter = "all" | "new" | "disabled" | "admins";

const name = (u: UserRow) => u.display_name || u.username;

/** The group shown for a user: their highest-ranked one. */
function topRole(u: UserRow, roles: RoleRow[]): RoleRow | undefined {
  const mine = roles.filter((r) => (u.role_ids ?? []).includes(r.id));
  return mine.sort(byRank)[0];
}

export function UsersPage() {
  const qc = useQueryClient();
  const { system, me } = useAuth();
  const discordOnly = Boolean(system?.discord_configured || system?.local_login_disabled);
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const sortedRoles = useMemo(() => [...(roles.data ?? [])].sort(byRank), [roles.data]);
  const assignable = sortedRoles.filter((r) => r.id !== "sys-superadmin" || me?.is_superadmin);

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (users.data ?? [])
      .filter((u) => !needle || `${u.username} ${u.display_name} ${(u.roles ?? []).join(" ")} ${u.household?.name ?? ""}`.toLowerCase().includes(needle))
      .filter((u) => {
        if (filter === "new") return isNewUser(u, discordOnly);
        if (filter === "disabled") return Boolean(u.disabled);
        if (filter === "admins") return u.is_admin;
        return true;
      })
      .sort((a, b) => {
        // Newest joiners first, then by name.
        const na = isNewUser(a, discordOnly) ? 1 : 0;
        const nb = isNewUser(b, discordOnly) ? 1 : 0;
        if (na !== nb) return nb - na;
        if (na && nb) return (joinedAt(b, discordOnly) ?? "").localeCompare(joinedAt(a, discordOnly) ?? "");
        return name(a).localeCompare(name(b), undefined, { sensitivity: "base" });
      });
  }, [users.data, q, filter, discordOnly]);

  const selectable = rows.filter((u) => !u.protected && u.id !== me?.id);
  const selected = selectable.filter((u) => picked.has(u.id));
  const newCount = (users.data ?? []).filter((u) => isNewUser(u, discordOnly)).length;

  const bulk = async (label: string, fn: (u: UserRow) => Promise<unknown>, confirmText?: string) => {
    if (!selected.length) return;
    if (confirmText && !window.confirm(confirmText)) return;
    setBusy(true);
    setNote(null);
    const failed: string[] = [];
    for (const u of selected) {
      try {
        await fn(u);
      } catch {
        failed.push(name(u));
      }
    }
    await qc.invalidateQueries({ queryKey: ["users"] });
    await qc.invalidateQueries({ queryKey: ["roles"] });
    setBusy(false);
    setPicked(new Set());
    setNote(
      failed.length
        ? { ok: false, text: `${label}: ${selected.length - failed.length} done, failed for ${failed.join(", ")}` }
        : { ok: true, text: `${label}: ${selected.length} account${selected.length === 1 ? "" : "s"}` },
    );
  };

  const toggleAll = () => setPicked(selected.length === selectable.length ? new Set() : new Set(selectable.map((u) => u.id)));

  return (
    <div className="space-y-4">
      <PageHeader
        title="Users"
        description={
          discordOnly
            ? "Everyone with an account. People join by signing in with Discord; new accounts are marked for a week."
            : "Everyone with an account. New accounts are marked for a week after they register."
        }
        actions={
          discordOnly ? null : (
            <button type="button" className={primaryBtn} onClick={() => setCreating(true)}>
              Create user
            </button>
          )
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <input className="min-w-0 flex-1 sm:max-w-xs" placeholder="Search people, groups or households" aria-label="Search users" value={q} onChange={(e) => setQ(e.target.value)} />
        <div role="tablist" className="flex gap-1 text-xs">
          {(
            [
              ["all", `All (${users.data?.length ?? 0})`],
              ["new", `New (${newCount})`],
              ["admins", "Administrators"],
              ["disabled", "Disabled"],
            ] as [Filter, string][]
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={filter === id}
              onClick={() => setFilter(id)}
              className={cn("rounded-full border px-3 py-1", filter === id ? "border-accent text-ink" : "border-line text-dim hover:text-ink")}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {selected.length ? (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-accent/40 bg-accent/5 px-3 py-2 text-xs">
          <span className="font-medium">{selected.length} selected</span>
          <button type="button" disabled={busy} className={secondaryBtn} onClick={() => void bulk("Enabled", (u) => api.patchUser(u.id, { disabled: false }))}>
            Enable
          </button>
          <button type="button" disabled={busy} className={secondaryBtn} onClick={() => void bulk("Disabled", (u) => api.patchUser(u.id, { disabled: true }))}>
            Disable
          </button>
          <select
            disabled={busy}
            value=""
            aria-label="Set group for selected"
            onChange={(e) => {
              const role = assignable.find((r) => r.id === e.target.value);
              if (role) void bulk(`Moved to ${role.name}`, (u) => api.patchUser(u.id, { role_ids: [role.id] }), `Put ${selected.length} account(s) in ${role.name}? Their other groups are removed.`);
            }}
          >
            <option value="">Set group…</option>
            {assignable.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
          <select
            disabled={busy}
            value=""
            aria-label="Set content restriction for selected"
            onChange={(e) => {
              if (e.target.value === "") return;
              const limit = Number(e.target.value);
              void bulk(`Content restriction ${ageLimitLabel(limit)}`, (u) => contentRatings.setUserLimit(u.id, limit));
            }}
          >
            <option value="">Content restriction…</option>
            {ageLimitOptions(0).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={busy}
            className="rounded-full border border-danger/50 px-4 py-1.5 text-sm text-danger"
            onClick={() => void bulk("Deleted", (u) => api.deleteUser(u.id), `Delete ${selected.length} account(s)? This cannot be undone.`)}
          >
            Delete
          </button>
          <button type="button" className="ml-auto text-dim hover:text-ink" onClick={() => setPicked(new Set())}>
            Clear
          </button>
        </div>
      ) : null}
      <NoteLine note={note} />

      <div className="h-scroll rounded-lg border border-line">
        <table className="w-full min-w-[820px] text-left text-sm">
          <thead className="border-b border-line text-xs text-dim">
            <tr>
              <th className="w-10 px-3 py-2">
                <input type="checkbox" aria-label="Select all" checked={selectable.length > 0 && selected.length === selectable.length} onChange={toggleAll} />
              </th>
              <th className="py-2 font-normal">Name</th>
              <th className="font-normal">Group</th>
              <th className="font-normal">Household</th>
              <th className="font-normal">Content</th>
              <th className="font-normal">{discordOnly ? "Joined (Discord)" : "Joined"}</th>
              <th className="pr-3 font-normal">Status</th>
            </tr>
          </thead>
          <tbody>
            {users.isLoading ? (
              <tr>
                <td colSpan={7} className="px-3 py-4 text-xs text-dim">
                  Loading people…
                </td>
              </tr>
            ) : null}
            {users.isSuccess && rows.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-3 py-4 text-xs text-dim">
                  Nobody matches.
                </td>
              </tr>
            ) : null}
            {rows.map((u) => {
              const role = topRole(u, sortedRoles);
              const joined = joinedAt(u, discordOnly);
              const locked = u.protected || u.id === me?.id;
              return (
                <tr key={u.id} className="cursor-pointer border-t border-line hover:bg-overlay" onClick={() => setOpen(u.id)}>
                  <td className="px-3 py-2" onClick={(e) => e.stopPropagation()}>
                    <input
                      type="checkbox"
                      aria-label={`Select ${name(u)}`}
                      disabled={locked}
                      title={locked ? (u.protected ? "The original Superadmin cannot be changed in bulk" : "Your own account") : undefined}
                      checked={picked.has(u.id)}
                      onChange={() =>
                        setPicked((cur) => {
                          const next = new Set(cur);
                          if (next.has(u.id)) next.delete(u.id);
                          else next.add(u.id);
                          return next;
                        })
                      }
                    />
                  </td>
                  <td className="py-2">
                    <span className="font-medium">{name(u)}</span>
                    <span className="ml-2 text-xs text-dim">{u.username}</span>
                    {isNewUser(u, discordOnly) ? <span className="ml-2 rounded bg-accent/15 px-1.5 py-0.5 text-[10px] text-accent">New</span> : null}
                  </td>
                  <td className="text-dim">
                    {role?.name ?? (u.is_admin ? "Administrator" : "None")}
                    {(u.role_ids ?? []).length > 1 ? <span className="ml-1 text-[10px]">+{(u.role_ids ?? []).length - 1}</span> : null}
                  </td>
                  <td className="text-dim">{u.household ? `${u.household.name} (${u.household.role})` : ""}</td>
                  <td className="text-dim">{u.content_age_limit ? ageLimitLabel(u.content_age_limit) : ""}</td>
                  <td className="text-dim tabular-nums">{joined ? new Date(joined).toLocaleDateString() : ""}</td>
                  <td className="pr-3">{u.disabled ? <span className="text-danger">Disabled</span> : <span className="text-ok">Active</span>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {open ? <UserModal id={open} roles={assignable} allRoles={sortedRoles} discordOnly={discordOnly} onClose={() => setOpen(null)} /> : null}
      {creating ? <CreateUserModal roles={assignable} onClose={() => setCreating(false)} /> : null}
    </div>
  );
}

function CreateUserModal({ roles, onClose }: { roles: RoleRow[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [roleId, setRoleId] = useState("sys-user");
  const [note, setNote] = useState<Note>(null);
  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    setNote(null);
    try {
      await api.createUser({ username, password, display_name: displayName, role_ids: roleId ? [roleId] : [] });
      await qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not create the account") });
    }
  };
  return (
    <AdminModal
      title="Create user"
      onClose={onClose}
      footer={
        <>
          <button type="button" className={secondaryBtn} onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={primaryBtn} disabled={!username || !password} onClick={() => void submit()}>
            Create
          </button>
        </>
      }
    >
      <form onSubmit={submit} className="space-y-2">
        <label className="block text-xs text-dim">
          Username
          <input className="mt-1 w-full" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus />
        </label>
        <label className="block text-xs text-dim">
          Display name
          <input className="mt-1 w-full" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
        </label>
        <label className="block text-xs text-dim">
          Password
          <input className="mt-1 w-full" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        <label className="block text-xs text-dim">
          Group
          <select className="mt-1 w-full" value={roleId} onChange={(e) => setRoleId(e.target.value)}>
            {roles.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        </label>
      </form>
      <NoteLine note={note} />
    </AdminModal>
  );
}

type Detail = UserRow & { content_restriction?: ContentRestriction };

/** Everything about one account, editable in place. */
function UserModal({ id, roles, allRoles, discordOnly, onClose }: { id: string; roles: RoleRow[]; allRoles: RoleRow[]; discordOnly: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const { me } = useAuth();
  const detail = useQuery({ queryKey: ["user", id], queryFn: () => api.getUser(id) as Promise<Detail> });
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const [note, setNote] = useState<Note>(null);
  const [display, setDisplay] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const u = detail.data;

  const run = async (fn: () => Promise<unknown>, ok: string, fallback: string) => {
    setNote(null);
    try {
      await fn();
      await Promise.all([qc.invalidateQueries({ queryKey: ["user", id] }), qc.invalidateQueries({ queryKey: ["users"] }), qc.invalidateQueries({ queryKey: ["roles"] })]);
      setNote({ ok: true, text: ok });
    } catch (e) {
      setNote({ ok: false, text: errText(e, fallback) });
    }
  };

  if (!u) {
    return (
      <AdminModal title="User" onClose={onClose}>
        <p className="text-xs text-dim">{detail.isError ? "This account could not be loaded." : "Loading…"}</p>
      </AdminModal>
    );
  }

  const locked = Boolean(u.protected);
  const self = u.id === me?.id;
  const current = topRole(u, allRoles);
  const grants = new Map((u.grants ?? []).map((g) => [g.library_id, g]));
  const joined = joinedAt(u, discordOnly);
  const extraGroups = allRoles.filter((r) => (u.role_ids ?? []).includes(r.id) && r.id !== current?.id);

  return (
    <AdminModal
      wide
      title={name(u)}
      description={
        <>
          {u.username}
          {isNewUser(u, discordOnly) ? " · new this week" : ""}
          {u.disabled ? " · disabled" : ""}
        </>
      }
      onClose={onClose}
      footer={
        <>
          {locked ? (
            <span className="mr-auto text-xs text-dim">The original Superadmin cannot be disabled or deleted.</span>
          ) : self ? (
            <span className="mr-auto text-xs text-dim">This is your account.</span>
          ) : (
            <>
              <button
                type="button"
                className="mr-auto text-xs text-danger"
                onClick={() => {
                  if (!window.confirm(`Delete ${name(u)}? This cannot be undone.`)) return;
                  void run(() => api.deleteUser(u.id), "Deleted", "Could not delete the account").then(onClose);
                }}
              >
                Delete account
              </button>
              <button type="button" className={secondaryBtn} onClick={() => void run(() => api.patchUser(u.id, { disabled: !u.disabled }), u.disabled ? "Enabled" : "Disabled", "Could not change the status")}>
                {u.disabled ? "Enable" : "Disable"}
              </button>
            </>
          )}
          <button type="button" className={primaryBtn} onClick={onClose}>
            Done
          </button>
        </>
      }
    >
      <section className="grid gap-x-4 gap-y-2 text-xs sm:grid-cols-[9rem_1fr]">
        <span className="text-dim">Display name</span>
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (display == null) return;
            void run(() => api.patchUser(u.id, { display_name: display }), "Name saved", "Could not save the name").then(() => setDisplay(null));
          }}
        >
          <input className="min-w-0 flex-1" value={display ?? u.display_name} onChange={(e) => setDisplay(e.target.value)} aria-label="Display name" />
          {display != null && display !== u.display_name ? (
            <button type="submit" className="text-accent">
              Save
            </button>
          ) : null}
        </form>
        <span className="text-dim">Joined</span>
        <span>
          {u.created_at ? new Date(u.created_at).toLocaleString() : "Unknown"}
          {u.discord_linked_at ? ` · Discord connected ${new Date(u.discord_linked_at).toLocaleString()}` : ""}
          {joined && isNewUser(u, discordOnly) ? <span className="ml-2 rounded bg-accent/15 px-1.5 py-0.5 text-[10px] text-accent">New</span> : null}
        </span>
        <span className="text-dim">Account ID</span>
        <code className="break-all text-dim">{u.id}</code>
        <span className="text-dim">Group</span>
        <div className="space-y-1">
          <select
            className="w-full sm:w-64"
            disabled={locked || self}
            value={current?.id ?? ""}
            aria-label="Group"
            onChange={(e) => {
              const next = e.target.value;
              if (!next) return;
              void run(() => api.patchUser(u.id, { role_ids: [next] }), "Group changed", "Could not change the group");
            }}
          >
            {current ? null : <option value="">No group</option>}
            {(locked || self ? allRoles : roles).map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
          {extraGroups.length ? <p className="text-[11px] text-dim">Also in {extraGroups.map((r) => r.name).join(", ")}; choosing a group here replaces them.</p> : null}
          {locked || self ? <p className="text-[11px] text-dim">{locked ? "The original Superadmin keeps its group." : "You cannot change your own group."}</p> : null}
        </div>
        <span className="text-dim">Content restriction</span>
        <div className="space-y-1">
          <select
            className="w-full sm:w-64"
            value={u.content_age_limit ?? 0}
            aria-label="Content restriction"
            onChange={(e) => void run(() => contentRatings.setUserLimit(u.id, Number(e.target.value)), "Restriction saved", "Could not save the restriction")}
          >
            {ageLimitOptions(u.content_age_limit ?? 0).map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          {u.content_restriction && u.content_restriction.household_limit > 0 ? (
            <p className="text-[11px] text-dim">
              Household limit {ageLimitLabel(u.content_restriction.household_limit)}; the stricter one applies ({ageLimitLabel(u.content_restriction.max_age)}).
            </p>
          ) : null}
        </div>
        <span className="text-dim">Household</span>
        <span>
          {u.household ? (
            <>
              {u.household.name} <span className="text-dim">({u.household.role})</span>{" "}
              <Link className="text-accent" to="/admin/households" onClick={onClose}>
                Manage
              </Link>
            </>
          ) : (
            <span className="text-dim">Not in a household</span>
          )}
        </span>
        <span className="text-dim">Discord</span>
        <span className="text-dim">{u.discord_id ? `Connected (${u.discord_id})` : "Not connected"}</span>
      </section>

      <section className="space-y-2 border-t border-line pt-3">
        <h3 className="text-xs font-medium">Library access</h3>
        {u.is_admin ? <p className="text-[11px] text-dim">Administrators see every library. Access below applies if they lose the administrator group.</p> : null}
        <p className="text-[11px] text-dim">Access given here is personal; groups can grant more under Library access.</p>
        <ul className="divide-y divide-line rounded-md border border-line text-xs">
          {(libs.data ?? []).map((l) => {
            const g = grants.get(l.id);
            return (
              <li key={l.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
                <label className="flex min-w-0 flex-1 items-center gap-2">
                  <input
                    type="checkbox"
                    checked={Boolean(g)}
                    onChange={() =>
                      void run(
                        () => (g ? api.deleteUserGrant(u.id, l.id) : api.setUserGrant(u.id, { library_id: l.id, can_download: false })),
                        g ? `Removed ${l.name}` : `Granted ${l.name}`,
                        "Could not change access",
                      )
                    }
                  />
                  <span className="truncate">{l.name}</span>
                </label>
                <label className={cn("flex items-center gap-1", !g && "opacity-40")}>
                  <input
                    type="checkbox"
                    disabled={!g}
                    checked={Boolean(g?.can_download)}
                    onChange={() => void run(() => api.setUserGrant(u.id, { library_id: l.id, can_download: !g?.can_download }), "Downloads updated", "Could not change downloads")}
                  />
                  Downloads
                </label>
              </li>
            );
          })}
        </ul>
      </section>

      {!discordOnly && !locked ? (
        <section className="space-y-2 border-t border-line pt-3">
          <h3 className="text-xs font-medium">Reset password</h3>
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (!password) return;
              void run(() => api.patchUser(u.id, { password }), "Password changed", "Could not change the password").then(() => setPassword(""));
            }}
          >
            <input className="min-w-0 flex-1 sm:max-w-xs" type="password" placeholder="New password" value={password} onChange={(e) => setPassword(e.target.value)} aria-label="New password" />
            <button type="submit" className={secondaryBtn} disabled={!password}>
              Set
            </button>
          </form>
        </section>
      ) : null}
      <NoteLine note={note} />
    </AdminModal>
  );
}
