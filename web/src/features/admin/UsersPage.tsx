import { FormEvent, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { ageLimitLabel, ageLimitOptions, contentRatings, type ContentRestriction } from "@/api/households";
import { useAuth } from "@/store/auth";
import { Card, CardGrid, PageHeader, secondaryBtn } from "./ui";

type RestrictedUser = { content_age_limit?: number; content_restriction?: ContentRestriction };

function ContentLimitField({
  userId,
  user,
  onSaved,
  onError,
}: {
  userId: string;
  user: RestrictedUser;
  onSaved: () => Promise<void>;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const limit = user.content_age_limit ?? 0;
  const effective = user.content_restriction;
  return (
    <div className="space-y-1">
      <label className="flex flex-wrap items-center gap-2 text-xs">
        <span className="font-medium">Content restriction</span>
        <select
          value={limit}
          disabled={busy}
          onChange={async (e) => {
            onError("");
            setBusy(true);
            try {
              await contentRatings.setUserLimit(userId, Number(e.target.value));
              await onSaved();
            } catch (e2) {
              onError(e2 instanceof Error ? e2.message : "could not save the restriction");
            } finally {
              setBusy(false);
            }
          }}
        >
          {ageLimitOptions(limit).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </label>
      {effective && effective.household_limit > 0 ? (
        <p className="text-[11px] text-dim">
          Household limit: {ageLimitLabel(effective.household_limit)}. The stricter limit applies ({ageLimitLabel(effective.max_age)}).
        </p>
      ) : null}
      {effective && effective.max_age > 0 ? (
        <p className="text-[11px] text-dim">
          Unrated titles are {effective.block_unrated ? "hidden" : "shown"} for restricted accounts.
        </p>
      ) : null}
    </div>
  );
}

export function UsersPage() {
  const qc = useQueryClient();
  const { system, me } = useAuth();
  const discordOnly = Boolean(system?.discord_configured || system?.local_login_disabled);
  const canAssignSuperadmin = Boolean(me?.is_superadmin);
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

  const libName = (id: string) => (libs.data ?? []).find((l) => l.id === id)?.name ?? id;
  const roleName = (id: string) => (roles.data ?? []).find((r) => r.id === id)?.name ?? id;

  return (
    <div className="space-y-4">
      <PageHeader title="Users" description="Accounts, their status and content restrictions. Groups and library access are edited on their own pages." />
      <CardGrid>
      <Card id="users-list" title="People">
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
                  {u.protected ? <span className="ml-2 text-[10px] text-accent">superadmin</span> : null}
                  {(u as RestrictedUser).content_age_limit ? (
                    <span className="ml-2 text-[10px] text-dim">max {(u as RestrictedUser).content_age_limit}</span>
                  ) : null}
                </span>
                <span className="text-[10px] text-dim">{(u.roles ?? []).join(", ") || (u.is_admin ? "admin" : "")}</span>
              </button>
            </li>
          ))}
        </ul>
      </Card>

      <div className="min-w-0 space-y-4">
        {discordOnly ? (
          <Card id="users-create" title="New accounts">
            <p className="text-xs text-dim">
              Discord sign-in is on, so local accounts cannot be created. New people join through Discord (and the server and role
              whitelist). Link Discord on an existing account under Settings, Connected accounts.
            </p>
          </Card>
        ) : (
        <Card id="users-create" title="Create user">
        <form onSubmit={onCreate} className="space-y-2">
          <input className="w-full" placeholder="Username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          <input className="w-full" placeholder="Display name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
          <input className="w-full" type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          <div className="flex flex-wrap gap-2 text-xs">
            {(roles.data ?? []).filter((r) => r.id !== "sys-superadmin" || canAssignSuperadmin).map((r) => (
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
          <button type="submit" className={secondaryBtn}>
            Create
          </button>
        </form>
        </Card>
        )}
        {err ? <p className="text-xs text-danger">{err}</p> : null}

        {detail.data ? (
          <Card id="users-detail" title={detail.data.display_name || detail.data.username} description={detail.data.username}>
            <ContentLimitField
              userId={detail.data.id}
              user={detail.data as RestrictedUser}
              onSaved={async () => {
                await qc.invalidateQueries({ queryKey: ["user", detail.data.id] });
                await qc.invalidateQueries({ queryKey: ["users"] });
              }}
              onError={setErr}
            />
            <div className="flex flex-wrap gap-3">
              {detail.data.protected ? null : (
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
              )}
              {detail.data.protected ? (
                <p className="text-xs text-dim">The original Superadmin cannot be deleted.</p>
              ) : (
                <button
                  type="button"
                  className="text-xs text-danger"
                  onClick={async () => {
                    if (!window.confirm(`Delete ${detail.data.display_name || detail.data.username}? This cannot be undone.`)) return;
                    try {
                      await api.deleteUser(detail.data.id);
                      setSelected(null);
                      await qc.invalidateQueries({ queryKey: ["users"] });
                    } catch (e2) {
                      setErr(e2 instanceof Error ? e2.message : "delete failed");
                    }
                  }}
                >
                  Delete user
                </button>
              )}
            </div>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 border-t border-line pt-3 text-xs">
              <dt className="text-dim">Groups</dt>
              <dd>
                {(detail.data.role_ids ?? []).length ? (detail.data.role_ids ?? []).map(roleName).join(", ") : "None"}{" "}
                <Link className="text-accent" to="/admin/roles">
                  Manage groups
                </Link>
              </dd>
              <dt className="text-dim">Library access</dt>
              <dd>
                {(detail.data.grants ?? []).length
                  ? (detail.data.grants ?? []).map((g) => `${libName(g.library_id)}${g.can_download ? " (downloads)" : ""}`).join(", ")
                  : detail.data.is_admin
                    ? "Every library (administrator)"
                    : "No direct access; groups may grant more"}{" "}
                <Link className="text-accent" to="/admin/media/access">
                  Manage access
                </Link>
              </dd>
            </dl>
          </Card>
        ) : (
          <p className="text-xs text-dim">Select a user to see their details.</p>
        )}
      </div>
      </CardGrid>
    </div>
  );
}
