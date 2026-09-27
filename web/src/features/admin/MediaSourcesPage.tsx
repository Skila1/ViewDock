import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type MediaSource, type MediaSourceLibrary } from "@/api/api";
import { Card, CardGrid, errText, NoteLine, PageHeader, Pill, primaryBtn, secondaryBtn, type Note } from "./ui";

const QUERY_KEY = ["admin-media-sources"];

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

function AddSource() {
  const qc = useQueryClient();
  const [f, setF] = useState({ name: "", url: "", username: "", password: "" });
  const [libraries, setLibraries] = useState<MediaSourceLibrary[] | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof f, v: string) => {
    setF((cur) => ({ ...cur, [k]: v }));
    setLibraries(null);
  };

  const test = async () => {
    setBusy(true);
    setNote(null);
    try {
      const res = await api.testMediaSource({ url: f.url.trim(), username: f.username.trim(), password: f.password });
      setLibraries(res.libraries);
      setSelected([]);
      setNote({ ok: true, text: `Connected to ${res.server_name || "Jellyfin"} ${res.version}` });
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
      await api.createMediaSource({ name: f.name.trim(), url: f.url.trim(), username: f.username.trim(), password: f.password, libraries: selected });
      setF({ name: "", url: "", username: "", password: "" });
      setLibraries(null);
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
      description="Use a dedicated Jellyfin account. ViewDock only reads from it and shows what that account is allowed to see."
    >
      <form onSubmit={onSubmit} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Server URL
            <input className="mt-1 w-full" value={f.url} onChange={(e) => set("url", e.target.value)} placeholder="https://jellyfin.example.com" required />
          </label>
          <label className="block text-xs text-dim">
            Display name (optional)
            <input className="mt-1 w-full" value={f.name} onChange={(e) => set("name", e.target.value)} />
          </label>
          <label className="block text-xs text-dim">
            Username
            <input className="mt-1 w-full" value={f.username} onChange={(e) => set("username", e.target.value)} autoComplete="off" required />
          </label>
          <label className="block text-xs text-dim">
            Password
            <input className="mt-1 w-full" type="password" value={f.password} onChange={(e) => set("password", e.target.value)} autoComplete="new-password" required />
          </label>
        </div>
        {libraries ? <LibraryPicker libraries={libraries} selected={selected} onChange={setSelected} /> : null}
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className={secondaryBtn} disabled={busy || !f.url || !f.username || !f.password} onClick={() => void test()}>
            Test connection
          </button>
          <button type="submit" className={primaryBtn} disabled={busy}>
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

function SourceCard({ source }: { source: MediaSource }) {
  const qc = useQueryClient();
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const [libraries, setLibraries] = useState<MediaSourceLibrary[] | null>(null);
  const [selected, setSelected] = useState<string[]>(source.libraries);
  const [creds, setCreds] = useState<{ username: string; password: string } | null>(null);

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

  return (
    <Card id={`media-source-${source.id}`} title={source.name} description={source.url} aside={statusPill(source)}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <dt className="text-dim">Account</dt>
        <dd>{source.username}</dd>
        <dt className="text-dim">Last sync</dt>
        <dd>{lastSync}</dd>
        <dt className="text-dim">Titles</dt>
        <dd>{source.item_count}</dd>
        <dt className="text-dim">Libraries</dt>
        <dd>{source.libraries.length === 0 ? "All" : `${source.libraries.length} selected`}</dd>
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
      {creds ? (
        <form
          className="grid gap-2 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            void run(
              async () => {
                await api.updateMediaSource(source.id, { username: creds.username.trim(), password: creds.password });
                setCreds(null);
              },
              "Credentials updated.",
              "the credentials were rejected",
            );
          }}
        >
          <label className="block text-xs text-dim">
            Username
            <input className="mt-1 w-full" value={creds.username} onChange={(e) => setCreds({ ...creds, username: e.target.value })} autoComplete="off" required />
          </label>
          <label className="block text-xs text-dim">
            New password
            <input className="mt-1 w-full" type="password" value={creds.password} onChange={(e) => setCreds({ ...creds, password: e.target.value })} autoComplete="new-password" required />
          </label>
          <div className="flex gap-2 sm:col-span-2">
            <button type="submit" className={primaryBtn} disabled={busy}>
              Save credentials
            </button>
            <button type="button" className={secondaryBtn} onClick={() => setCreds(null)}>
              Cancel
            </button>
          </div>
        </form>
      ) : null}
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
        <button type="button" className={secondaryBtn} disabled={busy} onClick={() => setCreds({ username: source.username, password: "" })}>
          Change credentials
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
