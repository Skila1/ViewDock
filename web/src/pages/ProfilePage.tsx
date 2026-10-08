import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type DesktopInfo } from "@/api/api";
import { desktopApp } from "@/lib/desktop";
import { clearCodecFailures, failedCodecs } from "@/api/profile";
import { cacheLimits, clearStreamCaches, DEFAULT_LIMITS, setCacheLimits, streamCacheUsage, type CacheLimits } from "@/playback/streamCache";
import {
  ageLimitLabel,
  ageLimitOptions,
  households,
  type AssignableHouseholdRole,
  type HouseholdInvite,
  type HouseholdMember,
} from "@/api/households";
import { Card } from "@/features/admin/ui";
import { useAuth } from "@/store/auth";

const MEMBER_ROLES: AssignableHouseholdRole[] = ["adult", "member", "child"];

function HouseholdSection({ userId, className }: { userId: string; className?: string }) {
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
    <Card id="household" title="Household" className={className}>
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
    </Card>
  );
}

export function ProfilePage() {
  const { me, system, boot } = useAuth();
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

  const localLogin = !(system?.discord_configured || system?.local_login_disabled);
  const showPin = localLogin || Boolean(me?.has_pin);

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">Profile</h1>
          <p className="text-sm text-dim">
            Signed in as {me?.username}
            {me?.roles?.length ? ` · ${me.roles.join(", ")}` : null}
          </p>
        </div>
        <div className="flex flex-wrap gap-3 text-sm">
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

      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}

      <div className="min-w-0 columns-1 gap-4 md:columns-2 xl:columns-3 [&>section]:mb-4 [&>section]:break-inside-avoid">
        <Card id="display-name" title="Display name" description="Shown to other members in watch parties and households.">
          <form onSubmit={saveProfile} className="space-y-3">
            <input className="w-full" value={display} aria-label="Display name" onChange={(e) => setDisplay(e.target.value)} />
            <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
              Save name
            </button>
          </form>
        </Card>

        <Card id="playback" title="Playback">
          <form onSubmit={savePrefs} className="space-y-3">
            <label className="block text-xs text-dim">
              Audio language
              <input
                className="mt-1 w-full"
                value={prefs.data?.audio_lang ?? ""}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, audio_lang: e.target.value })}
              />
            </label>
            <label className="block text-xs text-dim">
              Subtitle language
              <input
                className="mt-1 w-full"
                value={prefs.data?.subtitle_lang ?? ""}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, subtitle_lang: e.target.value })}
              />
            </label>
            <label className="block text-xs text-dim">
              Subtitles
              <select
                className="mt-1 w-full"
                value={prefs.data?.subtitle_mode ?? "auto"}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, subtitle_mode: e.target.value })}
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
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, autoplay: e.target.checked })}
              />
              Autoplay next episode
            </label>
            <label className="block text-xs text-dim">
              Up Next countdown (seconds, 0 waits for a click)
              <input
                type="number"
                min={0}
                max={60}
                className="mt-1 w-full"
                value={prefs.data?.upnext_seconds ?? 10}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, upnext_seconds: Math.max(0, Math.min(60, Number(e.target.value) || 0)) })}
              />
            </label>
            <label className="block text-xs text-dim">
              Playback speed
              <select
                className="mt-1 w-full"
                value={String(prefs.data?.playback_rate ?? 1)}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, playback_rate: Number(e.target.value) })}
              >
                {[0.75, 1, 1.25, 1.5, 1.75, 2].map((r) => (
                  <option key={r} value={String(r)}>
                    {r === 1 ? "Normal" : `${r}x`}
                  </option>
                ))}
              </select>
            </label>
            <label className="block text-xs text-dim">
              Quality
              <select
                className="mt-1 w-full"
                value={prefs.data?.quality || "auto"}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, quality: e.target.value })}
              >
                <option value="auto">Auto (original when this device can play it)</option>
                <option value="1080">1080p</option>
                <option value="720">720p</option>
                <option value="480">480p</option>
              </select>
            </label>
            <label className="block text-xs text-dim">
              When a title is both local and on a media server
              <select
                className="mt-1 w-full"
                value={prefs.data?.source_pref ?? ""}
                onChange={(e) => qc.setQueryData(["prefs"], { ...prefs.data, source_pref: e.target.value })}
              >
                <option value="">Play the local file first</option>
                <option value="remote">Play from the media server first</option>
              </select>
              <span className="mt-1 block text-[11px]">If the preferred copy is unavailable, the other one plays.</span>
            </label>
            <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
              Save playback
            </button>
          </form>
        </Card>

        <PlaybackCacheCard />
        <DesktopAppCard />

        {localLogin ? (
          <Card
            id="password"
            title={me?.has_password ? "Change password" : "Set a password"}
            description={me?.has_password ? undefined : "You signed in with Discord. Set a password to also use username login."}
          >
            <form
              className="space-y-3"
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
              {me?.has_password ? (
                <input
                  className="w-full"
                  type="password"
                  placeholder="Current password"
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                />
              ) : null}
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
          </Card>
        ) : null}

        {showPin ? (
          <Card
            id="pin"
            title="PIN lock"
            description={
              localLogin
                ? "Optional 4 to 8 digit PIN after idle. Leave empty and save to clear."
                : "Local sign-in is off while Discord sign-in is on. You can still remove the PIN set earlier."
            }
          >
            <form
              className="space-y-3"
              onSubmit={async (e) => {
                e.preventDefault();
                setErr("");
                try {
                  if (localLogin && pin) {
                    await api.setPin(pin);
                    setPin("");
                    setMsg("PIN set. Idle lock is 15 minutes.");
                  } else {
                    await api.clearPin();
                    setPin("");
                    setMsg("PIN cleared");
                  }
                  await boot();
                } catch (e2) {
                  setErr(e2 instanceof Error ? e2.message : "pin failed");
                }
              }}
            >
              {localLogin ? (
                <input
                  className="w-full"
                  inputMode="numeric"
                  placeholder={me?.has_pin ? "New PIN or empty to clear" : "PIN"}
                  value={pin}
                  onChange={(e) => setPin(e.target.value)}
                />
              ) : null}
              <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
                {localLogin && pin ? "Set PIN" : "Clear PIN"}
              </button>
            </form>
          </Card>
        ) : null}

        <HouseholdSection userId={me?.id ?? ""} />
      </div>

      <Card id="sessions" title="Sessions" description="Devices signed in to this account.">
        <ul className="divide-y divide-line rounded-md border border-line">
          {(sessions.data ?? []).map((sess) => (
            <li key={sess.id} className="flex flex-col gap-2 px-3 py-3 text-xs sm:flex-row sm:items-center sm:justify-between">
              <span className="min-w-0">
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
          {sessions.isLoading ? <li className="px-3 py-3 text-xs text-dim">Loading…</li> : null}
        </ul>
      </Card>
    </div>
  );
}

const GB = 1024 ** 3;

/** Starts a browser download of a same-origin file. */
function saveFile(url: string) {
  const a = document.createElement("a");
  a.href = url;
  a.download = "";
  document.body.appendChild(a);
  a.click();
  a.remove();
}

/**
 * ViewDock for Windows, built by this server with its own address. The
 * server packages it on demand, so a download waits until the package is
 * ready instead of fetching a placeholder.
 */
export function DesktopAppCard() {
  const app = desktopApp();
  const [want, setWant] = useState<"" | "exe" | "zip">("");
  const info = useQuery({
    queryKey: ["desktop-info"],
    queryFn: api.desktopInfo,
    refetchInterval: (q) => {
      const d = q.state.data as DesktopInfo | undefined;
      if (d?.preparing) return 5000;
      if (!want || !d?.windows) return false;
      return (want === "exe" ? d.windows.ready : d.windows.portable_ready) ? false : 3000;
    },
  });
  const win = info.data?.windows;
  const ready = want && win ? (want === "exe" ? win.ready : win.portable_ready) : false;
  useEffect(() => {
    if (!want || !win || !ready) return;
    saveFile(want === "exe" ? win.url : win.portable);
    setWant("");
  }, [want, win, ready]);

  const shown = Boolean(info.data?.available);
  useEffect(() => {
    // The account menu links here; the card appears only once the server answered.
    if (shown && window.location.hash === "#desktop-app") document.getElementById("desktop-app")?.scrollIntoView({ block: "start" });
  }, [shown]);

  if (!info.data?.available || !win) return null;
  const preparing = Boolean(want) && !ready;
  const building = Boolean(info.data.preparing);
  return (
    <Card
      id="desktop-app"
      title="ViewDock for Windows"
      description="The desktop app plays 4K originals with a much larger buffer than a browser allows, keeps playing in the background, and opens this server directly."
    >
      <div className="space-y-3 text-xs">
        {app ? (
          <p className="text-dim">
            You are using ViewDock for Windows {app.version}.
            {info.data.version && info.data.version !== app.version ? ` Version ${info.data.version} is available; the app offers it when it checks for updates.` : ""}
          </p>
        ) : (
          <>
            <p className="text-dim">
              Version {info.data.version}, connected to <span className="text-ink">{info.data.server_url}</span>. It installs for your Windows account
              only, with no administrator rights needed. Windows may warn that the app is unrecognised; choose More info, then Run anyway.
            </p>
            <div className="flex flex-wrap gap-3">
              <button
                type="button"
                className="rounded-md bg-accent px-3 py-2 text-sm text-white disabled:opacity-60"
                disabled={preparing || building}
                onClick={() => setWant("exe")}
              >
                {preparing && want === "exe" ? "Preparing download..." : "Download for Windows"}
              </button>
              {win.url !== win.portable ? (
                <button type="button" className="text-accent disabled:opacity-60" disabled={preparing || building} onClick={() => setWant("zip")}>
                  {preparing && want === "zip" ? "Preparing download..." : "Portable version (.zip)"}
                </button>
              ) : null}
            </div>
            {building ? <p className="text-dim">The server is downloading and building the app. This takes a few minutes the first time.</p> : null}
            {preparing && !building ? <p className="text-dim">The server is packaging the app for itself. This takes a minute or two the first time after an update.</p> : null}
            {info.data.error && !building ? <p className="text-danger">The app could not be built: {info.data.error}</p> : null}
          </>
        )}
      </div>
    </Card>
  );
}

/** This device's buffer cache: usage, limits and a way to clear it. Stored per device, since the cache is. */
function PlaybackCacheCard() {
  const [limits, setLimits] = useState(cacheLimits);
  const [used, setUsed] = useState(streamCacheUsage);
  const [note, setNote] = useState("");
  const save = (next: CacheLimits) => {
    setLimits(next);
    setCacheLimits(next);
    setNote("Saved for this device.");
  };
  return (
    <Card
      id="playback-cache"
      title="Playback cache (this device)"
      description="Media servers' streams are stored ahead of where you watch, then deleted a few minutes after you close the player."
    >
      <div className="space-y-3 text-xs">
        <p className="text-dim">
          In use now: <span className="text-ink">{(used / GB).toFixed(1)} GB</span>
        </p>
        <label className="block text-dim">
          Most for all titles (GB)
          <input
            type="number"
            min={1}
            max={500}
            className="mt-1 w-full"
            value={Math.round(limits.total / GB)}
            onChange={(e) => save({ ...limits, total: Math.max(1, Number(e.target.value) || 1) * GB })}
          />
        </label>
        <label className="block text-dim">
          Most per title (GB)
          <input
            type="number"
            min={1}
            max={100}
            className="mt-1 w-full"
            value={Math.round(limits.perTitle / GB)}
            onChange={(e) => save({ ...limits, perTitle: Math.max(1, Number(e.target.value) || 1) * GB })}
          />
        </label>
        <div className="flex flex-wrap gap-3">
          <button
            type="button"
            className="text-accent"
            onClick={async () => {
              await clearStreamCaches();
              setUsed(streamCacheUsage());
              setNote("Cache cleared.");
            }}
          >
            Clear cache now
          </button>
          <button
            type="button"
            className="text-accent"
            onClick={() => {
              save(DEFAULT_LIMITS);
              setNote("Limits reset.");
            }}
          >
            Reset limits
          </button>
          {failedCodecs().size ? (
            <button
              type="button"
              className="text-accent"
              onClick={() => {
                clearCodecFailures();
                setNote("This device will try every codec it reports again.");
              }}
            >
              Retry codecs this device failed ({[...failedCodecs()].join(", ").toUpperCase()})
            </button>
          ) : null}
        </div>
        {note ? <p className="text-dim">{note}</p> : null}
      </div>
    </Card>
  );
}
