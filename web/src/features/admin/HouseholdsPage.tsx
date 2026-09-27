import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import {
  adminHouseholds,
  ageLimitLabel,
  ageLimitOptions,
  type AssignableHouseholdRole,
  type Household,
  type HouseholdInvite,
  type HouseholdMember,
} from "@/api/households";

const MEMBER_ROLES: AssignableHouseholdRole[] = ["adult", "member", "child"];

export function HouseholdsPage() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["admin-households"], queryFn: adminHouseholds.list });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const [selected, setSelected] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [ownerId, setOwnerId] = useState("");
  const [err, setErr] = useState("");

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      const res = await adminHouseholds.create({ name: name.trim(), owner_id: ownerId });
      setName("");
      setOwnerId("");
      await qc.invalidateQueries({ queryKey: ["admin-households"] });
      setSelected(res.household.id);
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "create failed");
    }
  };

  const households = list.data ?? [];

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <div className="space-y-4">
        <div>
          <h1 className="text-base font-medium">Households</h1>
          <p className="text-sm text-dim">
            People join a household by accepting an invite. Administrators can reorganise existing members, set content
            restrictions and transfer ownership.
          </p>
        </div>
        {list.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
        {list.isError ? <p className="text-xs text-danger">Households are unavailable.</p> : null}
        {!list.isLoading && !list.isError && households.length === 0 ? (
          <p className="text-xs text-dim">No households yet.</p>
        ) : null}
        {households.length ? (
          <ul className="divide-y divide-line rounded-md border border-line">
            {households.map((h) => (
              <li key={h.id}>
                <button
                  type="button"
                  className={`flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-overlay ${selected === h.id ? "bg-overlay" : ""}`}
                  onClick={() => setSelected(h.id)}
                >
                  <span>
                    {h.name}
                    <span className="ml-2 text-xs text-dim">{h.owner_username || "no owner"}</span>
                  </span>
                  <span className="text-[10px] text-dim">
                    {h.member_count ?? 0} member{h.member_count === 1 ? "" : "s"}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : null}

        <form onSubmit={create} className="space-y-2">
          <h2 className="text-sm font-medium">Create household</h2>
          <input className="w-full" placeholder="Household name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} required />
          <select className="w-full" value={ownerId} onChange={(e) => setOwnerId(e.target.value)} required>
            <option value="">Select an owner (not already in a household).</option>
            {(users.data ?? []).map((u) => (
              <option key={u.id} value={u.id}>
                {u.display_name || u.username} ({u.username})
              </option>
            ))}
          </select>
          <button type="submit" className="rounded-md bg-accent px-3 py-1.5 text-sm text-white">
            Create
          </button>
        </form>
        {err ? <p className="text-xs text-danger">{err}</p> : null}
      </div>

      <div>
        {selected ? (
          <HouseholdDetail
            key={selected}
            id={selected}
            others={households.filter((h) => h.id !== selected)}
            onDeleted={() => setSelected(null)}
          />
        ) : (
          <p className="text-xs text-dim">Select a household to manage members.</p>
        )}
      </div>
    </div>
  );
}

function HouseholdDetail({ id, others, onDeleted }: { id: string; others: Household[]; onDeleted: () => void }) {
  const qc = useQueryClient();
  const detail = useQuery({ queryKey: ["admin-household", id], queryFn: () => adminHouseholds.get(id) });
  const [rename, setRename] = useState<string | null>(null);
  const [inviteRole, setInviteRole] = useState<AssignableHouseholdRole>("member");
  const [inviteAge, setInviteAge] = useState(0);
  const [invite, setInvite] = useState<HouseholdInvite | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["admin-household", id] });
    await qc.invalidateQueries({ queryKey: ["admin-households"] });
  };

  const run = async (fn: () => Promise<unknown>, done: string): Promise<boolean> => {
    setErr("");
    setMsg("");
    try {
      await fn();
      await refresh();
      setMsg(done);
      return true;
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "update failed");
      return false;
    }
  };

  if (detail.isLoading) return <p className="text-xs text-dim">Loading…</p>;
  if (detail.isError || !detail.data) return <p className="text-xs text-danger">This household is unavailable.</p>;

  const { household, members } = detail.data;
  const transferable = members.filter((m) => m.role !== "owner" && !m.temporary && !m.expires_at);

  const updateMember = (m: HouseholdMember, body: { role?: AssignableHouseholdRole; age_limit?: number }) =>
    run(() => adminHouseholds.updateMember(id, m.id, body), "Member updated");

  return (
    <div className="space-y-4 rounded-md border border-line p-3">
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          if (rename === null) return;
          void run(() => adminHouseholds.update(id, { name: rename.trim() }), "Household renamed").then((ok) => ok && setRename(null));
        }}
      >
        {rename === null ? (
          <>
            <h2 className="text-sm font-medium">{household.name}</h2>
            <button type="button" className="text-xs text-accent" onClick={() => setRename(household.name)}>
              Rename
            </button>
          </>
        ) : (
          <>
            <input className="min-w-0 flex-1" value={rename} maxLength={100} onChange={(e) => setRename(e.target.value)} required />
            <button type="submit" className="btn-green rounded-full px-3 py-1 text-xs">
              Save
            </button>
            <button type="button" className="text-xs text-dim" onClick={() => setRename(null)}>
              Cancel
            </button>
          </>
        )}
      </form>

      <ul className="divide-y divide-line rounded-md border border-line">
        {members.map((m) => (
          <li key={m.id} className="space-y-2 px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm">{m.display_name || m.username}</span>
              <span className="text-dim">{m.username}</span>
              <span className="rounded bg-[var(--chip)] px-1.5 py-0.5 text-[10px] text-[var(--chip-text)]">{m.role}</span>
              {m.temporary ? <span className="text-[10px] text-dim">guest</span> : null}
              {m.age_limit ? <span className="text-[10px] text-dim">{ageLimitLabel(m.age_limit)}</span> : null}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {m.role !== "owner" ? (
                <select
                  value={m.role}
                  aria-label={`Role for ${m.username}`}
                  onChange={(e) => void updateMember(m, { role: e.target.value as AssignableHouseholdRole })}
                >
                  {MEMBER_ROLES.map((r) => (
                    <option key={r} value={r}>
                      {r}
                    </option>
                  ))}
                </select>
              ) : null}
              <select
                value={m.age_limit}
                aria-label={`Content restriction for ${m.username}`}
                onChange={(e) => void updateMember(m, { age_limit: Number(e.target.value) })}
              >
                {ageLimitOptions(m.age_limit).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
              {m.role !== "owner" && others.length ? (
                <select
                  value=""
                  aria-label={`Move ${m.username}`}
                  onChange={(e) => {
                    const target = e.target.value;
                    if (!target) return;
                    const to = others.find((h) => h.id === target);
                    if (!window.confirm(`Move ${m.display_name || m.username} to ${to?.name ?? "the selected household"}?`)) return;
                    void run(() => adminHouseholds.moveMember(id, m.id, target), "Member moved");
                  }}
                >
                  <option value="">Move to…</option>
                  {others.map((h) => (
                    <option key={h.id} value={h.id}>
                      {h.name}
                    </option>
                  ))}
                </select>
              ) : null}
              {m.role !== "owner" ? (
                <button
                  type="button"
                  className="text-danger"
                  onClick={() => {
                    if (!window.confirm(`Remove ${m.display_name || m.username} from ${household.name}?`)) return;
                    void run(() => adminHouseholds.removeMember(id, m.id), "Member removed");
                  }}
                >
                  Remove
                </button>
              ) : null}
            </div>
          </li>
        ))}
      </ul>

      {transferable.length ? (
        <label className="flex flex-wrap items-center gap-2 text-xs">
          <span className="font-medium">Transfer ownership</span>
          <select
            value=""
            onChange={(e) => {
              const next = transferable.find((m) => m.id === e.target.value);
              if (!next) return;
              if (!window.confirm(`Make ${next.display_name || next.username} the owner of ${household.name}?`)) return;
              void run(() => adminHouseholds.update(id, { owner_id: next.id }), "Ownership transferred");
            }}
          >
            <option value="">Select a member.</option>
            {transferable.map((m) => (
              <option key={m.id} value={m.id}>
                {m.display_name || m.username}
              </option>
            ))}
          </select>
        </label>
      ) : null}

      <form
        className="flex flex-wrap items-center gap-2 text-xs"
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          setInvite(null);
          void run(async () => {
            setInvite(await adminHouseholds.createInvite(id, { role: inviteRole, age_limit: inviteAge }));
          }, "Invite created. It works once and must be accepted by the invitee.");
        }}
      >
        <span className="font-medium">Invite</span>
        <select value={inviteRole} onChange={(e) => setInviteRole(e.target.value as AssignableHouseholdRole)} aria-label="Invite role">
          {MEMBER_ROLES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
        <select value={inviteAge} onChange={(e) => setInviteAge(Number(e.target.value))} aria-label="Invite content restriction">
          {ageLimitOptions(inviteAge).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <button type="submit" className="btn-green rounded-full px-3 py-1">
          Create invite
        </button>
      </form>
      {invite ? (
        <p className="break-all text-xs">
          Invite code <code className="rounded bg-raised px-1">{invite.token}</code> (expires {new Date(invite.expires_at).toLocaleDateString()})
        </p>
      ) : null}

      <button
        type="button"
        className="text-xs text-danger"
        onClick={async () => {
          if (!window.confirm(`Delete ${household.name}? Members stay as individual accounts. This cannot be undone.`)) return;
          if (await run(() => adminHouseholds.remove(id), "Household deleted")) onDeleted();
        }}
      >
        Delete household
      </button>

      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </div>
  );
}
