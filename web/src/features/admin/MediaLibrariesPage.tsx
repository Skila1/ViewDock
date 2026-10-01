import { FormEvent, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { ContentType, Library } from "@/types/api.gen";
import { MoveContentDialog } from "./MoveContentDialog";
import { CONTENT_TYPES, managedFolder, typeLabel } from "./moveContent";
import { Card, CardGrid, NoteLine, PageHeader, Pill, errText, inputCls, primaryBtn, secondaryBtn, type Note } from "./ui";

function useRefresh() {
  const qc = useQueryClient();
  return async () => {
    await qc.invalidateQueries({ queryKey: ["libraries"] });
    await qc.invalidateQueries({ queryKey: ["movies"] });
    await qc.invalidateQueries({ queryKey: ["series"] });
  };
}

function LibraryFields({
  name,
  path,
  type,
  onName,
  onPath,
  onType,
  folderHint,
}: {
  name: string;
  path: string;
  type: ContentType;
  onName: (v: string) => void;
  onPath: (v: string) => void;
  onType: (v: ContentType) => void;
  folderHint: string;
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <label className="block text-sm">
        Name
        <input className={inputCls} value={name} onChange={(e) => onName(e.target.value)} required />
      </label>
      <label className="block text-sm">
        Content
        <select className={inputCls} value={type} onChange={(e) => onType(e.target.value as ContentType)}>
          {CONTENT_TYPES.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label} ({c.holds})
            </option>
          ))}
        </select>
      </label>
      <details className="text-sm sm:col-span-2" open={path !== ""}>
        <summary className="cursor-pointer text-xs text-dim">Folder: {path.trim() || folderHint}</summary>
        <label className="mt-2 block text-xs text-dim">
          Custom folder (optional). A name like <span className="font-mono">kids/movies</span> is placed inside the media folder.
          <input className={`${inputCls} font-mono`} value={path} onChange={(e) => onPath(e.target.value)} placeholder={folderHint} />
        </label>
      </details>
    </div>
  );
}

function useMediaDir() {
  const system = useQuery({ queryKey: ["system"], queryFn: api.getSystem, staleTime: 60_000 });
  return system.data?.media_dir;
}

function AddLibrary() {
  const refresh = useRefresh();
  const mediaDir = useMediaDir();
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [type, setType] = useState<ContentType>("movies");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setNote(null);
    try {
      const lib = await api.createLibrary({ name: name.trim(), path: path.trim(), content_type: type });
      await api.scanLibrary(lib.id).catch(() => undefined);
      setName("");
      setPath("");
      setNote({ ok: true, text: `Created ${lib.name} in ${lib.path}. It is ready for uploads and the first scan has started.` });
      await refresh();
    } catch (err) {
      setNote({ ok: false, text: errText(err, "the library could not be created") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="media-add-library"
      title="Add a library"
      description="ViewDock creates the library's folder and sets its permissions itself. New libraries are visible to the User group; change that in Library access."
    >
      <form className="space-y-3" onSubmit={submit}>
        <LibraryFields
          name={name}
          path={path}
          type={type}
          onName={setName}
          onPath={setPath}
          onType={setType}
          folderHint={managedFolder(mediaDir, name || "New library")}
        />
        <button type="submit" className={`${primaryBtn} disabled:opacity-50`} disabled={busy || !name.trim()}>
          {busy ? "Creating…" : "Create library"}
        </button>
        <NoteLine note={note} />
      </form>
    </Card>
  );
}

function LibraryCard({ lib, titles, libraries }: { lib: Library; titles: number; libraries: Library[] }) {
  const refresh = useRefresh();
  const mediaDir = useMediaDir();
  const [moving, setMoving] = useState(false);
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(lib.name);
  const [path, setPath] = useState(lib.path);
  const [type, setType] = useState<ContentType>(lib.content_type);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const run = async (fn: () => Promise<unknown>, ok: string, fail: string) => {
    setBusy(true);
    setNote(null);
    try {
      await fn();
      setNote({ ok: true, text: ok });
      await refresh();
      return true;
    } catch (err) {
      setNote({ ok: false, text: errText(err, fail) });
      return false;
    } finally {
      setBusy(false);
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await run(
      () => api.patchLibrary(lib.id, { name: name.trim(), path: path.trim(), content_type: type }),
      path.trim() !== lib.path ? "Saved. Files were not moved; use Move content to move titles between libraries." : "Saved.",
      "the library could not be saved",
    );
    if (ok) setEditing(false);
  };

  return (
    <Card
      id={`library-${lib.id}`}
      title={lib.name}
      aside={
        <div className="flex flex-wrap gap-1">
          <Pill tone="dim">{typeLabel(lib.content_type)}</Pill>
          <Pill tone="accent">{titles} titles</Pill>
        </div>
      }
    >
      {editing ? (
        <form className="space-y-3" onSubmit={save}>
          <LibraryFields name={name} path={path} type={type} onName={setName} onPath={setPath} onType={setType} folderHint={managedFolder(mediaDir, name)} />
          <div className="flex flex-wrap gap-2">
            <button type="submit" className={`${primaryBtn} disabled:opacity-50`} disabled={busy || !name.trim()}>
              Save
            </button>
            <button
              type="button"
              className={secondaryBtn}
              onClick={() => {
                setEditing(false);
                setName(lib.name);
                setPath(lib.path);
                setType(lib.content_type);
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <p className="break-all font-mono text-xs text-dim">{lib.path}</p>
      )}
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={lib.uploads_enabled}
          disabled={busy}
          onChange={(e) =>
            void run(
              () => api.patchLibrary(lib.id, { uploads_enabled: e.target.checked }),
              e.target.checked ? "Uploads are allowed." : "Uploads are turned off.",
              "the upload setting could not be saved",
            )
          }
        />
        Allow uploads into this library
      </label>
      <div className="flex flex-wrap gap-2">
        <button type="button" className={secondaryBtn} disabled={busy} onClick={() => void run(() => api.scanLibrary(lib.id), "Scan started.", "the scan could not start")}>
          Scan now
        </button>
        {!editing ? (
          <button type="button" className={secondaryBtn} onClick={() => setEditing(true)}>
            Edit
          </button>
        ) : null}
        <Link className={secondaryBtn} to={`/admin/media/titles?library=${encodeURIComponent(lib.id)}`}>
          Titles
        </Link>
        <button type="button" className={secondaryBtn} disabled={busy || titles === 0} onClick={() => setMoving(true)}>
          Move content…
        </button>
        {confirmDelete ? (
          <>
            <button
              type="button"
              className="rounded-full border border-danger/40 bg-danger/10 px-4 py-1.5 text-sm text-danger disabled:opacity-50"
              disabled={busy}
              onClick={() => void run(() => api.deleteLibrary(lib.id), `Deleted ${lib.name}.`, "the library could not be deleted")}
            >
              Delete {lib.name}
            </button>
            <button type="button" className={secondaryBtn} onClick={() => setConfirmDelete(false)}>
              Keep it
            </button>
          </>
        ) : (
          <button type="button" className={`${secondaryBtn} text-danger`} onClick={() => setConfirmDelete(true)}>
            Delete
          </button>
        )}
      </div>
      {confirmDelete ? (
        <p className="text-xs text-danger">
          This removes the library, its titles, watch history for them and its access rules from ViewDock. Files on disk are not touched.
        </p>
      ) : null}
      <NoteLine note={note} />
      {moving ? <MoveContentDialog open={moving} onOpenChange={setMoving} libraries={libraries} source={lib} /> : null}
    </Card>
  );
}

export function MediaLibrariesPage() {
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const movies = useQuery({ queryKey: ["movies"], queryFn: api.listMovies });
  const series = useQuery({ queryKey: ["series"], queryFn: api.listSeries });
  const counts = new Map<string, number>();
  for (const t of [...(movies.data ?? []), ...(series.data ?? [])]) {
    if (t.library_id) counts.set(t.library_id, (counts.get(t.library_id) ?? 0) + 1);
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="Libraries"
        description={
          <>
            Each library is a folder ViewDock creates and manages inside its media folder. Use Move content to move titles between compatible
            libraries. Jellyfin libraries are managed from{" "}
            <Link className="text-accent" to="/admin/media/sources">
              Jellyfin servers
            </Link>
            .
          </>
        }
      />
      {libs.isLoading ? <p className="text-sm text-dim">Loading libraries…</p> : null}
      {libs.isError ? <p className="text-sm text-danger">{errText(libs.error, "libraries could not be loaded")}</p> : null}
      <CardGrid>
        {(libs.data ?? []).map((lib) => (
          <LibraryCard key={`${lib.id}-${lib.updated_at ?? ""}`} lib={lib} titles={counts.get(lib.id) ?? 0} libraries={libs.data ?? []} />
        ))}
        <AddLibrary />
      </CardGrid>
    </div>
  );
}
