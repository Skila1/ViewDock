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

function LibraryPicker({
  libraries,
  selected,
  onChange,
}: {
  libraries: MediaSourceLibrary[];
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  if (libraries.length === 0) return <p className="text-xs text-dim">This account cannot see any movie or TV libraries.</p>;
  const all = selected.length === 0;
  return (
    <fieldset className="space-y-1">
      <legend className="text-xs text-dim">Libraries to import (none selected imports all)</legend>
      {libraries.map((lib) => (
        <label key={lib.id} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={all || selected.includes(lib.id)}
            onChange={(e) => {
              const base = all ? libraries.map((l) => l.id) : selected;
              const next = e.target.checked ? [...base, lib.id] : base.filter((id) => id !== lib.id);
              onChange(next.length === libraries.length ? [] : next);
            }}
          />
          {lib.name}
          <span className="text-[11px] text-dim">{lib.collection_type === "tvshows" ? "TV" : lib.collection_type === "movies" ? "Movies" : "Mixed"}</span>
        </label>
      ))}
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
        <span className="block text-[11px] text-dim">0 means no limit.</span>
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
  const [libraries, setLibraries] = useState<MediaSourceLibrary[] | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [policy, setPolicy] = useState<MediaSourcePolicy>(DEFAULT_POLICY);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<Draft>) => {
    setF((cur) => ({ ...cur, ...patch }));
    setLibraries(null);
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
      setLibraries(res.account ? res.libraries : null);
      setSelected([]);
      const who = res.account ? ` as ${res.account.name}` : "";
      setNote({ ok: true, text: `Connected to ${res.server_name || "Jellyfin"} ${res.version}${who}` });
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
      await api.createMediaSource({ name: f.name.trim(), url: f.url.trim(), ...credentials(f), libraries: selected, policy });
      setF(EMPTY);
      setUsers(null);
      setLibraries(null);
      setPolicy(DEFAULT_POLICY);
      setNote({ ok: true, text: "Server connected. The first sync has started." });
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the server could not be connected") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="media-source-add"
      title="Connect a Jellyfin server"
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
        {libraries ? <LibraryPicker libraries={libraries} selected={selected} onChange={setSelected} /> : null}
        <PolicyEditor value={policy} onChange={setPolicy} />
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className={secondaryBtn} disabled={busy || !ready} onClick={() => void test(f)}>
            Test connection
          </button>
          <button type="submit" className={primaryBtn} disabled={busy || (f.mode === "api_key" && !f.user)}>
            {busy ? "Working…" : "Connect"}
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
  const [libraries, setLibraries] = useState<MediaSourceLibrary[] | null>(null);
  const [selected, setSelected] = useState<string[]>(source.libraries);
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
        setLibraries(res.libraries);
        setNote({ ok: true, text: `Connected to ${res.server_name || "Jellyfin"} ${res.version}` });
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
        <dt className="text-dim">Libraries</dt>
        <dd>{source.libraries.length === 0 ? "All" : `${source.libraries.length} selected`}</dd>
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
      {libraries ? (
        <div className="space-y-2">
          <LibraryPicker libraries={libraries} selected={selected} onChange={setSelected} />
          <button
            type="button"
            className={secondaryBtn}
            disabled={busy}
            onClick={() => void run(() => api.updateMediaSource(source.id, { libraries: selected }), "Libraries saved. A resync has started.", "the libraries could not be saved")}
          >
            Save libraries
          </button>
        </div>
      ) : null}
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
          Test and choose libraries
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
