import { FormEvent, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import {
  ageLimitLabel,
  ageLimitOptions,
  households,
  type AssignableHouseholdRole,
  type HouseholdInvite,
  type HouseholdMember,
} from "@/api/households";
import { useAuth } from "@/store/auth";

const MEMBER_ROLES: AssignableHouseholdRole[] = ["adult", "member", "child"];

function HouseholdSection({ userId }: { userId: string }) {
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["household"], queryFn: households.mine });
  const restriction = useQuery({ queryKey: ["content-restriction"], queryFn: households.myRestriction });
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [inviteRole, setInviteRole] = useState<AssignableHouseholdRole>("member");
  const [inviteAge, setInviteAge] = useState(0);
  const [invite, setInvite] = useState<HouseholdInvite | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const household = view.data?.household ?? null;
  const members = view.data?.members ?? [];
  const isOwner = Boolean(household && household.owner_id === userId);

  const run = async (fn: () => Promise<unknown>, done: string): Promise<boolean> => {
    setErr("");
    setMsg("");
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: ["household"] });
      await qc.invalidateQueries({ queryKey: ["content-restriction"] });
      setMsg(done);
      return true;
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "household update failed");
      return false;
    }
  };

  const updateMember = (m: HouseholdMember, patch: { role?: AssignableHouseholdRole; age_limit?: number }) =>
    run(
      () =>
        households.updateMember({
          user_id: m.id,
          role: patch.role ?? (m.role === "owner" ? "adult" : m.role),
          age_limit: patch.age_limit ?? m.age_limit,
        }),
      "Member updated",
    );

  return (
    <div className="space-y-3">
      <h2 className="text-sm font-medium">Household</h2>
      {restriction.data && restriction.data.max_age > 0 ? (
        <p className="text-xs text-dim">
          Content on this account is limited to {ageLimitLabel(restriction.data.max_age).toLowerCase()}.
          {restriction.data.block_unrated ? " Unrated titles are hidden." : null}
        </p>
      ) : null}
      {view.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
      {view.isError ? <p className="text-xs text-danger">Household details are unavailable.</p> : null}

      {!view.isLoading && !view.isError && !household ? (
        <div className="space-y-3">
          <form
            className="flex gap-2"
            onSubmit={(e: FormEvent) => {
              e.preventDefault();
              void run(() => households.create(name.trim()), "Household created").then((ok) => ok && setName(""));
            }}
          >
            <input className="min-w-0 flex-1" placeholder="Household name" value={name} onChange={(e) => setName(e.target.value)} required />
            <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
              Create
            </button>
          </form>
          <form
            className="flex gap-2"
            onSubmit={(e: FormEvent) => {
              e.preventDefault();
              void run(() => households.acceptInvite(token.trim()), "Joined household").then((ok) => ok && setToken(""));
            }}
          >
            <input className="min-w-0 flex-1" placeholder="Invite code" value={token} onChange={(e) => setToken(e.target.value)} required />
            <button type="submit" className="rounded-full border border-line px-4 py-1.5 text-sm">
              Join
            </button>
          </form>
        </div>
      ) : null}

      {household ? (
        <div className="space-y-3">
          <p className="text-xs text-dim">
            {household.name}
            {isOwner ? " · you are the owner" : null}
          </p>
          <ul className="divide-y divide-line rounded-md border border-line">
            {members.map((m) => (
              <li key={m.id} className="flex flex-col gap-2 px-3 py-2 text-xs sm:flex-row sm:items-center sm:justify-between">
                <span>
                  {m.display_name || m.username}
                  <span className="ml-2 text-dim">{m.role}</span>
                  {m.age_limit ? <span className="ml-2 text-dim">{ageLimitLabel(m.age_limit)}</span> : null}
                </span>
                {isOwner && m.role !== "owner" ? (
                  <span className="flex flex-wrap items-center gap-2">
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
                    <button
                      type="button"
                      className="text-danger"
                      onClick={() => {
                        if (!window.confirm(`Remove ${m.display_name || m.username} from the household?`)) return;
                        void run(() => households.removeMember(m.id), "Member removed");
                      }}
                    >
                      Remove
                    </button>
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
          {isOwner ? (
            <form
              className="flex flex-wrap items-center gap-2 text-xs"
              onSubmit={(e: FormEvent) => {
                e.preventDefault();
                setInvite(null);
                void run(async () => {
                  setInvite(await households.createInvite({ role: inviteRole, age_limit: inviteAge }));
                }, "Invite created. Share the code; it works once.");
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
          ) : null}
          {invite ? (
            <p className="break-all text-xs">
              Invite code <code className="rounded bg-raised px-1">{invite.token}</code> (expires {new Date(invite.expires_at).toLocaleDateString()})
            </p>
          ) : null}
        </div>
      ) : null}
      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </div>
  );
}

export function ProfilePage() {
  const { me, boot } = useAuth();
  const qc = useQueryClient();
  const prefs = useQuery({ queryKey: ["prefs"], queryFn: api.getPreferences });
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.listSessions });
  const [display, setDisplay] = useState(me?.display_name ?? "");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [pin, setPin] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  const saveProfile = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    setMsg("");
    try {
      await api.patchMe({ display_name: display });
      await boot();
      setMsg("Profile saved");
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "save failed");
    }
  };

  const savePrefs = async (e: FormEvent) => {
    e.preventDefault();
    if (!prefs.data) return;
    setErr("");
    try {
      await api.putPreferences(prefs.data);
      await qc.invalidateQueries({ queryKey: ["prefs"] });
      setMsg("Playback preferences saved");
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "save failed");
    }
  };

  return (
    <div className="mx-auto max-w-xl space-y-8">
      <div>
        <h1 className="text-lg font-semibold">Profile</h1>
        <p className="text-sm text-dim">
          Signed in as {me?.username}
          {me?.roles?.length ? ` · ${me.roles.join(", ")}` : null}
        </p>
        <div className="mt-2 flex flex-wrap gap-3 text-sm">
          <Link to="/settings/connected" className="text-accent">
            Connected services
          </Link>
          {me?.is_admin ? (
            <Link to="/admin" className="text-accent">
              Admin
            </Link>
          ) : null}
        </div>
      </div>

      <form onSubmit={saveProfile} className="space-y-2">
        <h2 className="text-sm font-medium">Display name</h2>
        <input className="w-full" value={display} onChange={(e) => setDisplay(e.target.value)} />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Save name
        </button>
      </form>

      <form onSubmit={savePrefs} className="space-y-2">
        <h2 className="text-sm font-medium">Playback</h2>
        <label className="block text-xs text-dim">
          Audio language
          <input
            className="mt-1 w-full"
            value={prefs.data?.audio_lang ?? ""}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, audio_lang: e.target.value })
            }
          />
        </label>
        <label className="block text-xs text-dim">
          Subtitle language
          <input
            className="mt-1 w-full"
            value={prefs.data?.subtitle_lang ?? ""}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, subtitle_lang: e.target.value })
            }
          />
        </label>
        <label className="block text-xs text-dim">
          Subtitles
          <select
            className="mt-1 w-full"
            value={prefs.data?.subtitle_mode ?? "auto"}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, subtitle_mode: e.target.value })
            }
          >
            <option value="auto">Auto</option>
            <option value="always">Always</option>
            <option value="off">Off</option>
          </select>
        </label>
        <label className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={prefs.data?.autoplay ?? true}
            onChange={(e) =>
              qc.setQueryData(["prefs"], { ...prefs.data, autoplay: e.target.checked })
            }
          />
          Autoplay next episode
        </label>
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Save playback
        </button>
      </form>

      <form
        className="space-y-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr("");
          try {
            await api.changePassword({ current, next });
            setCurrent("");
            setNext("");
            setMsg("Password updated. Other sessions were signed out.");
          } catch (e2) {
            setErr(e2 instanceof Error ? e2.message : "password failed");
          }
        }}
      >
        <h2 className="text-sm font-medium">{me?.has_password ? "Change password" : "Set a password"}</h2>
        {me?.has_password ? (
          <input
            className="w-full"
            type="password"
            placeholder="Current password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        ) : (
          <p className="text-xs text-dim">You signed in with Discord. Set a password to also use username login.</p>
        )}
        <input
          className="w-full"
          type="password"
          placeholder="New password (8+ characters)"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Update password
        </button>
      </form>

      <form
        className="space-y-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr("");
          try {
            if (pin) {
              await api.setPin(pin);
              setPin("");
              setMsg("PIN set. Idle lock is 15 minutes.");
            } else {
              await api.clearPin();
              setMsg("PIN cleared");
            }
            await boot();
          } catch (e2) {
            setErr(e2 instanceof Error ? e2.message : "pin failed");
          }
        }}
      >
        <h2 className="text-sm font-medium">PIN lock</h2>
        <p className="text-xs text-dim">Optional 4–8 digit PIN after idle. Leave empty and save to clear.</p>
        <input
          className="w-full"
          inputMode="numeric"
          placeholder={me?.has_pin ? "New PIN or empty to clear" : "PIN"}
          value={pin}
          onChange={(e) => setPin(e.target.value)}
        />
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          {pin ? "Set PIN" : "Clear PIN"}
        </button>
      </form>

      <HouseholdSection userId={me?.id ?? ""} />

      <div>
        <h2 className="mb-2 text-sm font-medium">Sessions</h2>
        <ul className="divide-y divide-line rounded-md border border-line">
          {(sessions.data ?? []).map((sess) => (
            <li key={sess.id} className="flex flex-col gap-2 px-3 py-3 text-xs sm:flex-row sm:items-center sm:justify-between">
              <span>
                {sess.current ? <span className="text-accent">This device · </span> : null}
                {sess.ip || "unknown IP"}
                <span className="ml-2 text-dim">{sess.user_agent?.slice(0, 48)}</span>
              </span>
              {!sess.current ? (
                <button
                  type="button"
                  className="text-danger"
                  onClick={async () => {
                    await api.revokeSession(sess.id);
                    await qc.invalidateQueries({ queryKey: ["sessions"] });
                  }}
                >
                  Revoke
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      </div>

      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </div>
  );
}
