import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  type MediaSource,
  type MediaSourceAuthMode,
  type MediaSourceLibrary,
  type MediaSourcePolicy,
  type MediaSourceUser,
} from "@/api/api";
import { Card, CardGrid, errText, NoteLine, PageHeader, Pill, primaryBtn, secondaryBtn, type Note } from "./ui";

const QUERY_KEY = ["admin-media-sources"];

const DEFAULT_POLICY: MediaSourcePolicy = { images: true, stream: true, transcode: true, activity_log: false, max_streams: 0 };

const sameId = (a: string, b: string) => a.replace(/-/g, "").toLowerCase() === b.replace(/-/g, "").toLowerCase();

/** The kind of content a Jellyfin library holds, as shown to admins. */
export function libraryType(lib: MediaSourceLibrary): string {
  if (/anime/i.test(lib.name)) return "Anime";
  if (lib.collection_type === "movies") return "Movies";
  if (lib.collection_type === "tvshows") return "TV shows";
  return "Mixed";
}

const TYPE_ORDER = ["Movies", "TV shows", "Anime", "Mixed"];

/**
 * Chooses what a source imports: everything its Jellyfin user can see
 * (an empty list, which also picks up libraries added later), or only
 * the libraries ticked here.
 */
function SyncSelection({
  libraries,
  selected,
  picking,
  onPicking,
  onChange,
}: {
  libraries: MediaSourceLibrary[];
  selected: string[];
  picking: boolean;
  onPicking: (picking: boolean) => void;
  onChange: (ids: string[]) => void;
}) {
  const setPicking = onPicking;
  if (libraries.length === 0) {
    return <p className="text-xs text-dim">The Jellyfin user ViewDock browses as cannot see any movie or TV libraries.</p>;
  }
  const groups = TYPE_ORDER.map((type) => [type, libraries.filter((l) => libraryType(l) === type)] as const).filter(([, list]) => list.length > 0);
  const toggle = (id: string, on: boolean) => {
    const next = on ? [...new Set([...selected, id])] : selected.filter((x) => x !== id);
    onChange(next);
  };
  const toggleGroup = (list: readonly MediaSourceLibrary[], on: boolean) => {
    const ids = list.map((l) => l.id);
    onChange(on ? [...new Set([...selected, ...ids])] : selected.filter((x) => !ids.includes(x)));
  };
  return (
    <fieldset className="space-y-2">
      <label className="flex items-center gap-2 text-sm">
        <input
          type="radio"
          checked={!picking}
          onChange={() => {
            setPicking(false);
            onChange([]);
          }}
        />
        Everything available ({libraries.length} {libraries.length === 1 ? "library" : "libraries"}, and any added later)
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="radio"
          checked={picking}
          onChange={() => {
            setPicking(true);
            onChange(selected.length ? selected : libraries.map((l) => l.id));
          }}
        />
        Only the libraries I choose
      </label>
      {picking ? (
        <div className="grid gap-3 pl-6 sm:grid-cols-2">
          {groups.map(([type, list]) => {
            const allOn = list.every((l) => selected.includes(l.id));
            return (
              <div key={type} className="space-y-1">
                <label className="flex items-center gap-2 text-xs font-medium text-ink">
                  <input type="checkbox" checked={allOn} onChange={(e) => toggleGroup(list, e.target.checked)} />
                  {type}
                </label>
                {list.map((lib) => (
                  <label key={lib.id} className="flex items-center gap-2 pl-5 text-sm">
                    <input type="checkbox" checked={selected.includes(lib.id)} onChange={(e) => toggle(lib.id, e.target.checked)} />
                    {lib.name}
                  </label>
                ))}
              </div>
            );
          })}
        </div>
      ) : null}
      {picking && selected.length === 0 ? <p className="pl-6 text-xs text-warn">Choose at least one library.</p> : null}
    </fieldset>
  );
}

/** The usage restrictions ViewDock enforces on itself for one source. */
function PolicyEditor({ value, onChange }: { value: MediaSourcePolicy; onChange: (p: MediaSourcePolicy) => void }) {
  const set = (patch: Partial<MediaSourcePolicy>) => onChange({ ...value, ...patch });
  const row = (checked: boolean, label: string, hint: string, change: (v: boolean) => void, disabled = false) => (
    <label className="flex items-start gap-2 text-sm">
      <input type="checkbox" className="mt-1" checked={checked} disabled={disabled} onChange={(e) => change(e.target.checked)} />
      <span>
        {label}
        <span className="block text-[11px] text-dim">{hint}</span>
      </span>
    </label>
  );
  return (
    <fieldset className="space-y-2 rounded-md border border-line p-3">
      <legend className="px-1 text-xs text-dim">Usage restrictions (ViewDock refuses anything not allowed here)</legend>
      {row(true, "Read the library catalogue", "Required: titles, seasons, episodes and descriptions.", () => {}, true)}
      {row(value.images, "Download posters and artwork", "Off leaves Jellyfin titles without posters.", (v) => set({ images: v }))}
      {row(value.stream, "Stream media", "Off lists titles but they cannot be played.", (v) => set({ stream: v, transcode: v && value.transcode }))}
      {row(
        value.transcode,
        "Allow Jellyfin transcoding",
        "Off plays only files browsers handle as is (MP4 with H.264); other files will not play.",
        (v) => set({ transcode: v }),
        !value.stream,
      )}
      {row(
        value.activity_log,
        "Read ViewDock's entries in the Jellyfin activity log",
        "Only entries for the Jellyfin user ViewDock uses, without IP addresses. Needs an admin account or API key.",
        (v) => set({ activity_log: v }),
      )}
      <label className="block text-sm">
        Maximum concurrent streams
        <input
          className="mt-1 block w-28"
          type="number"
          min={0}
          max={100}
          value={value.max_streams}
          disabled={!value.stream}
          onChange={(e) => set({ max_streams: Math.max(0, Math.min(100, Number(e.target.value) || 0)) })}
        />
        <span className="block text-[11px] text-dim">ViewDock's own cap, separate from Jellyfin. Every viewer uses one stream, including each member of a watch party or Discord activity. 0 means no limit.</span>
      </label>
    </fieldset>
  );
}

function AuthModeSwitch({ value, onChange }: { value: MediaSourceAuthMode; onChange: (m: MediaSourceAuthMode) => void }) {
  return (
    <fieldset className="flex flex-wrap gap-4 text-sm">
      <legend className="mb-1 text-xs text-dim">Sign in with</legend>
      <label className="flex items-center gap-2">
        <input type="radio" checked={value === "password"} onChange={() => onChange("password")} />
        Jellyfin account (recommended)
      </label>
      <label className="flex items-center gap-2">
        <input type="radio" checked={value === "api_key"} onChange={() => onChange("api_key")} />
        API key
      </label>
    </fieldset>
  );
}

function AdminWarning({ user, mode }: { user?: MediaSourceUser; mode: MediaSourceAuthMode }) {
  if (mode === "api_key")
    return (
      <p className="text-xs text-warn">
        Jellyfin API keys have full administrator access to that server. ViewDock stores the key encrypted and only makes the requests the restrictions
        below allow, but a dedicated non-admin account is safer.
      </p>
    );
  if (user?.is_admin)
    return <p className="text-xs text-warn">This account is a Jellyfin administrator. A dedicated non-admin account limits what the credentials can do.</p>;
  return null;
}

function UserPicker({ users, value, onChange }: { users: MediaSourceUser[]; value: string; onChange: (id: string) => void }) {
  return (
    <label className="block text-xs text-dim">
      Browse as Jellyfin user
      <select className="mt-1 w-full" value={value} onChange={(e) => onChange(e.target.value)} required>
        <option value="">Choose a user</option>
        {users.map((u) => (
          <option key={u.id} value={u.id}>
            {u.name}
            {u.is_admin ? " (administrator)" : ""}
          </option>
        ))}
      </select>
      <span className="mt-1 block text-[11px]">ViewDock only sees and plays what this user can access.</span>
    </label>
  );
}

type Draft = { name: string; url: string; mode: MediaSourceAuthMode; username: string; password: string; apiKey: string; user: string };
const EMPTY: Draft = { name: "", url: "", mode: "password", username: "", password: "", apiKey: "", user: "" };

function AddSource() {
  const qc = useQueryClient();
  const [f, setF] = useState<Draft>(EMPTY);
  const [users, setUsers] = useState<MediaSourceUser[] | null>(null);
  const [account, setAccount] = useState<MediaSourceUser | undefined>();
  const [policy, setPolicy] = useState<MediaSourcePolicy>(DEFAULT_POLICY);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<Draft>) => {
    setF((cur) => ({ ...cur, ...patch }));
    setAccount(undefined);
    if (!("user" in patch)) setUsers(null);
  };
  const credentials = (d: Draft) =>
    d.mode === "api_key"
      ? { auth_mode: d.mode, api_key: d.apiKey.trim(), remote_user_id: d.user }
      : { auth_mode: d.mode, username: d.username.trim(), password: d.password };
  const ready = f.url && (f.mode === "api_key" ? f.apiKey : f.username && f.password);

  const test = async (d: Draft) => {
    setBusy(true);
    setNote(null);
    try {
      const res = await api.testMediaSource({ url: d.url.trim(), ...credentials(d) });
      if (res.users) setUsers(res.users);
      setAccount(res.account);
      const who = res.account ? ` as ${res.account.name}` : "";
      const seen = res.account ? `, ${res.libraries.length} ${res.libraries.length === 1 ? "library" : "libraries"} available` : "";
      const next = d.mode === "api_key" && !d.user ? " Choose the Jellyfin user, then save." : " Nothing is saved yet: press Save to add it.";
      setNote({ ok: true, text: `Test passed: ${res.server_name || "Jellyfin"} ${res.version}${who}${seen}.${next}` });
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the connection test failed") });
    } finally {
      setBusy(false);
    }
  };

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setNote(null);
    try {
      const src = await api.createMediaSource({ name: f.name.trim(), url: f.url.trim(), ...credentials(f), libraries: [], policy });
      setF(EMPTY);
      setUsers(null);
      setAccount(undefined);
      setPolicy(DEFAULT_POLICY);
      setNote({ ok: true, text: `Saved ${src.name}. It syncs everything available; choose what to sync in its box above.` });
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
      window.setTimeout(() => document.getElementById(`media-source-${src.id}`)?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the server could not be saved") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="media-source-add"
      title="Add a Jellyfin server"
      description="ViewDock only reads from the server, and only in the ways the usage restrictions allow."
    >
      <form onSubmit={onSubmit} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Server URL
            <input className="mt-1 w-full" value={f.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://jellyfin.example.com" required />
          </label>
          <label className="block text-xs text-dim">
            Display name (optional)
            <input className="mt-1 w-full" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </label>
        </div>
        <AuthModeSwitch value={f.mode} onChange={(mode) => set({ mode })} />
        {f.mode === "password" ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-xs text-dim">
              Username
              <input className="mt-1 w-full" value={f.username} onChange={(e) => set({ username: e.target.value })} autoComplete="off" required />
            </label>
            <label className="block text-xs text-dim">
              Password
              <input className="mt-1 w-full" type="password" value={f.password} onChange={(e) => set({ password: e.target.value })} autoComplete="new-password" required />
            </label>
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block text-xs text-dim">
              API key
              <input className="mt-1 w-full" type="password" value={f.apiKey} onChange={(e) => set({ apiKey: e.target.value })} autoComplete="off" required />
            </label>
            {users ? (
              <UserPicker
                users={users}
                value={f.user}
                onChange={(user) => {
                  const next = { ...f, user };
                  set({ user });
                  if (user) void test(next);
                }}
              />
            ) : null}
          </div>
        )}
        <AdminWarning user={account} mode={f.mode} />
        <PolicyEditor value={policy} onChange={setPolicy} />
        <p className="text-[11px] text-dim">After saving, choose which Jellyfin libraries (movies, TV, anime and so on) to sync in the server's box.</p>
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className={secondaryBtn} disabled={busy || !ready} onClick={() => void test(f)}>
            Test connection
          </button>
          <button type="submit" className={primaryBtn} disabled={busy || !ready || (f.mode === "api_key" && !f.user)}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
        <NoteLine note={note} />
      </form>
    </Card>
  );
}

function statusPill(s: MediaSource) {
  if (!s.enabled) return <Pill tone="dim">Disabled</Pill>;
  if (s.syncing || s.status === "syncing") return <Pill tone="accent">Syncing</Pill>;
  if (s.status === "error") return <Pill tone="danger">Error</Pill>;
  if (s.status === "ok") return <Pill tone="ok">Synced</Pill>;
  return <Pill tone="dim">Pending</Pill>;
}

function CredentialsForm({ source, onDone }: { source: MediaSource; onDone: (note: Note) => void }) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<MediaSourceAuthMode>(source.auth_mode);
  const [username, setUsername] = useState(source.username);
  const [password, setPassword] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [users, setUsers] = useState<MediaSourceUser[] | null>(null);
  const [user, setUser] = useState(source.remote_user_id);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const sameMode = mode === source.auth_mode;

  const loadUsers = async () => {
    setBusy(true);
    setNote(null);
    try {
      const res = await api.testMediaSource(apiKey ? { url: source.url, auth_mode: "api_key", api_key: apiKey } : { id: source.id, auth_mode: "api_key" });
      setUsers(res.users ?? []);
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the API key was rejected") });
    } finally {
      setBusy(false);
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setNote(null);
    try {
      await api.updateMediaSource(
        source.id,
        mode === "api_key"
          ? { auth_mode: mode, api_key: apiKey || undefined, remote_user_id: user }
          : { auth_mode: mode, username: username.trim(), password: password || undefined },
      );
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
      onDone({ ok: true, text: "Credentials updated. A resync has started." });
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the credentials were rejected") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="space-y-3 rounded-md border border-line p-3" onSubmit={save}>
      <AuthModeSwitch value={mode} onChange={setMode} />
      {mode === "password" ? (
        <div className="grid gap-2 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Username
            <input className="mt-1 w-full" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" required />
          </label>
          <label className="block text-xs text-dim">
            {sameMode ? "New password (leave empty to keep)" : "Password"}
            <input
              className="mt-1 w-full"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              required={!sameMode}
            />
          </label>
        </div>
      ) : (
        <div className="grid gap-2 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            {sameMode ? "New API key (leave empty to keep)" : "API key"}
            <input className="mt-1 w-full" type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} autoComplete="off" required={!sameMode} />
          </label>
          {users ? (
            <UserPicker users={users} value={users.some((u) => sameId(u.id, user)) ? users.find((u) => sameId(u.id, user))!.id : ""} onChange={setUser} />
          ) : (
            <div className="flex items-end">
              <button type="button" className={secondaryBtn} disabled={busy || (!sameMode && !apiKey)} onClick={() => void loadUsers()}>
                Choose user
              </button>
            </div>
          )}
        </div>
      )}
      <AdminWarning mode={mode} />
      <div className="flex gap-2">
        <button type="submit" className={primaryBtn} disabled={busy || (mode === "api_key" && !user)}>
          Save credentials
        </button>
        <button type="button" className={secondaryBtn} onClick={() => onDone(null)}>
          Cancel
        </button>
      </div>
      <NoteLine note={note} />
    </form>
  );
}

function UsageLog({ source }: { source: MediaSource }) {
  const events = useQuery({ queryKey: [...QUERY_KEY, source.id, "events"], queryFn: () => api.mediaSourceEvents(source.id) });
  const activity = useQuery({
    queryKey: [...QUERY_KEY, source.id, "activity"],
    queryFn: () => api.mediaSourceActivity(source.id),
    enabled: false,
  });
  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-xs font-semibold">What ViewDock did (last 30 days)</h3>
        {events.isError ? <p className="text-xs text-danger">{errText(events.error, "the usage log could not be loaded")}</p> : null}
        <ul className="mt-1 max-h-60 space-y-1 overflow-y-auto text-xs">
          {(events.data ?? []).map((e, i) => (
            <li key={i} className="flex gap-2">
              <span className="shrink-0 text-dim">{new Date(e.created_at).toLocaleString()}</span>
              <span className={e.ok ? "" : "text-danger"}>
                {e.kind.replace(/_/g, " ")}: {e.detail}
              </span>
            </li>
          ))}
          {events.data?.length === 0 ? <li className="text-dim">Nothing recorded yet.</li> : null}
        </ul>
      </div>
      {source.policy.activity_log ? (
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-xs font-semibold">Jellyfin activity for {source.remote_user_name || "the ViewDock user"} (last 7 days)</h3>
            <button type="button" className="text-xs text-accent" disabled={activity.isFetching} onClick={() => void activity.refetch()}>
              {activity.isFetching ? "Loading…" : activity.data ? "Refresh" : "Load"}
            </button>
          </div>
          {activity.isError ? <p className="text-xs text-danger">{errText(activity.error, "the activity log could not be read")}</p> : null}
          {activity.data ? (
            <ul className="mt-1 max-h-60 space-y-1 overflow-y-auto text-xs">
              {activity.data.map((a, i) => (
                <li key={i} className="flex gap-2">
                  <span className="shrink-0 text-dim">{new Date(a.date).toLocaleString()}</span>
                  <span>{a.name}</span>
                </li>
              ))}
              {activity.data.length === 0 ? <li className="text-dim">No entries.</li> : null}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function SourceCard({ source }: { source: MediaSource }) {
  const qc = useQueryClient();
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const libraries = useQuery({
    queryKey: [...QUERY_KEY, source.id, "libraries"],
    queryFn: () => api.mediaSourceLibraries(source.id),
    enabled: source.enabled,
    retry: false,
  });
  const [selected, setSelected] = useState<string[]>(source.libraries);
  const [picking, setPicking] = useState(source.libraries.length > 0);
  const savedKey = [...source.libraries].sort().join(",");
  const dirty = picking ? [...selected].sort().join(",") !== savedKey : savedKey !== "";
  const [creds, setCreds] = useState(false);
  const [policy, setPolicy] = useState<MediaSourcePolicy | null>(null);
  const [log, setLog] = useState(false);

  const run = async (fn: () => Promise<unknown>, ok: string, fail: string) => {
    setBusy(true);
    setNote(null);
    try {
      await fn();
      if (ok) setNote({ ok: true, text: ok });
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
    } catch (e) {
      setNote({ ok: false, text: errText(e, fail) });
    } finally {
      setBusy(false);
    }
  };

  const test = () =>
    run(
      async () => {
        const res = await api.testMediaSource({ id: source.id });
        setNote({ ok: true, text: `Test passed: ${res.server_name || "Jellyfin"} ${res.version}, ${res.libraries.length} libraries available.` });
        await qc.invalidateQueries({ queryKey: [...QUERY_KEY, source.id, "libraries"] });
      },
      "",
      "the connection test failed",
    );

  const lastSync = source.last_sync_at ? new Date(source.last_sync_at).toLocaleString() : "never";
  const who = source.auth_mode === "api_key" ? `API key, browsing as ${source.remote_user_name || "unknown user"}` : source.username;

  return (
    <Card id={`media-source-${source.id}`} title={source.name} description={source.url} aside={statusPill(source)}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <dt className="text-dim">Credentials</dt>
        <dd>{who}</dd>
        <dt className="text-dim">Last sync</dt>
        <dd>{lastSync}</dd>
        <dt className="text-dim">Titles</dt>
        <dd>{source.item_count}</dd>
        <dt className="text-dim">Allowed</dt>
        <dd>
          {[
            "catalogue",
            source.policy.images && "posters",
            source.policy.stream && "streaming",
            source.policy.transcode && "transcoding",
            source.policy.activity_log && "activity log",
          ]
            .filter(Boolean)
            .join(", ")}
          {source.policy.stream && source.policy.max_streams > 0 ? `; at most ${source.policy.max_streams} streams` : ""}
        </dd>
      </dl>
      {source.last_error ? <p className="text-xs text-danger">{source.last_error}</p> : null}
      <section className="space-y-2 rounded-md border border-line p-3" aria-label="What to sync">
        <p className="text-sm font-medium">What to sync</p>
        {!source.enabled ? <p className="text-xs text-dim">Enable the source to choose what it syncs.</p> : null}
        {libraries.isLoading ? <p className="text-xs text-dim">Loading the server's libraries…</p> : null}
        {libraries.isError ? <p className="text-xs text-danger">{errText(libraries.error, "the server's libraries could not be loaded")}</p> : null}
        {libraries.data ? (
          <>
            <SyncSelection
              libraries={libraries.data}
              selected={selected}
              picking={picking}
              onPicking={setPicking}
              onChange={setSelected}
            />
            <button
              type="button"
              className={primaryBtn}
              disabled={busy || !dirty || (picking && selected.length === 0)}
              onClick={() =>
                void run(
                  () => api.updateMediaSource(source.id, { libraries: picking ? selected : [] }),
                  "Saved. A resync has started; titles from libraries you left out are removed.",
                  "the selection could not be saved",
                )
              }
            >
              Save
            </button>
          </>
        ) : null}
      </section>
      {policy ? (
        <div className="space-y-2">
          <PolicyEditor value={policy} onChange={setPolicy} />
          <div className="flex gap-2">
            <button
              type="button"
              className={primaryBtn}
              disabled={busy}
              onClick={() =>
                void run(
                  async () => {
                    await api.updateMediaSource(source.id, { policy });
                    setPolicy(null);
                  },
                  "Restrictions saved. Active streams from this source were stopped.",
                  "the restrictions could not be saved",
                )
              }
            >
              Save restrictions
            </button>
            <button type="button" className={secondaryBtn} onClick={() => setPolicy(null)}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
      {creds ? (
        <CredentialsForm
          source={source}
          onDone={(n) => {
            setCreds(false);
            setNote(n);
          }}
        />
      ) : null}
      {log ? <UsageLog source={source} /> : null}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          className={secondaryBtn}
          disabled={busy || !source.enabled || source.syncing}
          onClick={() => void run(() => api.syncMediaSource(source.id), "Resync started.", "the resync could not start")}
        >
          Resync
        </button>
        <button type="button" className={secondaryBtn} disabled={busy} onClick={() => void test()}>
          Test connection
        </button>
        <button type="button" className={secondaryBtn} disabled={busy} onClick={() => setPolicy(policy ? null : source.policy)}>
          Restrictions
        </button>
        <button type="button" className={secondaryBtn} disabled={busy} onClick={() => setCreds(!creds)}>
          Change credentials
        </button>
        <button type="button" className={secondaryBtn} onClick={() => setLog(!log)}>
          {log ? "Hide usage log" : "Usage log"}
        </button>
        <button
          type="button"
          className={secondaryBtn}
          disabled={busy}
          onClick={() =>
            void run(
              () => api.updateMediaSource(source.id, { enabled: !source.enabled }),
              source.enabled ? "Disabled. Its titles are hidden from everyone." : "Enabled. A resync has started.",
              "the source could not be updated",
            )
          }
        >
          {source.enabled ? "Disable" : "Enable"}
        </button>
        <button
          type="button"
          className="rounded-full border border-line px-4 py-1.5 text-sm text-danger"
          disabled={busy}
          onClick={() => {
            if (!window.confirm(`Remove ${source.name}? Its titles are removed from ViewDock. Nothing changes on the Jellyfin server.`)) return;
            void run(() => api.deleteMediaSource(source.id), "", "the source could not be removed");
          }}
        >
          Remove
        </button>
      </div>
      <NoteLine note={note} />
    </Card>
  );
}

export function MediaSourcesPage() {
  const q = useQuery({
    queryKey: QUERY_KEY,
    queryFn: api.listMediaSources,
    refetchInterval: (query) => ((query.state.data ?? []).some((s) => s.syncing || s.status === "syncing") ? 3000 : false),
  });
  return (
    <div className="space-y-4">
      <PageHeader
        title="Media sources"
        description="Jellyfin servers whose movies and shows appear for every ViewDock user. Titles also in a local library are listed once and play locally by default."
      />
      {q.isError ? <p className="text-sm text-danger">{errText(q.error, "media sources could not be loaded")}</p> : null}
      <CardGrid>
        {(q.data ?? []).map((s) => (
          <SourceCard key={s.id} source={s} />
        ))}
        <AddSource />
      </CardGrid>
      {q.isLoading ? <p className="text-sm text-dim">Loading…</p> : null}
    </div>
  );
}
