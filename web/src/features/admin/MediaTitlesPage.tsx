import { FormEvent, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ImageUp } from "lucide-react";
import { api } from "@/api/api";
import type { Movie, Series } from "@/types/api.gen";
import { ContentRatingControl } from "./ContentRatingControl";
import { NoteLine, PageHeader, Pill, errText, secondaryBtn, type Note } from "./ui";

type Kind = "movie" | "series";
type Row = { kind: Kind; title: Movie | Series };
type Filter = "all" | "unmatched";

const PAGE = 60;

function TitleRow({ row, libraryName }: { row: Row; libraryName: string }) {
  const qc = useQueryClient();
  const { kind, title } = row;
  const [matching, setMatching] = useState(false);
  const [tmdb, setTmdb] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);
  const file = useRef<HTMLInputElement>(null);
  const listKey = kind === "movie" ? ["movies"] : ["series"];

  const run = async (fn: () => Promise<unknown>, ok: string, fail: string) => {
    setBusy(true);
    setNote(null);
    try {
      await fn();
      setNote({ ok: true, text: ok });
      await qc.invalidateQueries({ queryKey: listKey });
      await qc.invalidateQueries({ queryKey: [kind, title.id] });
      return true;
    } catch (err) {
      setNote({ ok: false, text: errText(err, fail) });
      return false;
    } finally {
      setBusy(false);
    }
  };

  const match = async (e: FormEvent) => {
    e.preventDefault();
    const id = Number(tmdb.trim());
    if (!Number.isInteger(id) || id <= 0) {
      setNote({ ok: false, text: "Enter the numeric TMDB id from the title's themoviedb.org address." });
      return;
    }
    if (await run(() => api.matchTitle(kind, title.id, id), "Matched. Metadata and artwork are refreshing.", "the title could not be matched")) {
      setMatching(false);
      setTmdb("");
    }
  };

  const href = kind === "movie" ? `/movies/${title.id}` : `/tv/${title.id}`;

  return (
    <li className="space-y-2 rounded-lg border border-line bg-raised p-3">
      <div className="flex gap-3">
        <div className="h-24 w-16 shrink-0 overflow-hidden rounded bg-overlay">
          {title.poster_url ? <img src={title.poster_url} alt="" loading="lazy" className="h-full w-full object-cover" /> : null}
        </div>
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <Link to={href} className="truncate text-sm font-medium hover:text-accent">
              {title.title}
            </Link>
            {title.year ? <span className="text-xs text-dim">{title.year}</span> : null}
            <Pill tone="dim">{kind === "movie" ? "Movie" : "Series"}</Pill>
            {title.unmatched ? <Pill tone="warn">Unmatched</Pill> : null}
          </div>
          <p className="text-xs text-dim">
            {libraryName}
            {title.metadata_source ? `, metadata from ${title.metadata_source}` : ""}
          </p>
          <ContentRatingControl kind={kind} id={title.id} title={title} />
          <div className="flex flex-wrap gap-2 pt-1">
            <button type="button" className={secondaryBtn} disabled={busy} onClick={() => setMatching((v) => !v)}>
              {matching ? "Cancel match" : "Match on TMDB"}
            </button>
            <button type="button" className={`${secondaryBtn} inline-flex items-center gap-1`} disabled={busy} onClick={() => file.current?.click()}>
              <ImageUp className="h-3.5 w-3.5" />
              Upload poster
            </button>
            <input
              ref={file}
              type="file"
              accept="image/jpeg,image/png,image/webp"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void run(() => api.uploadPoster(kind, title.id, f), "Poster uploaded.", "the poster could not be uploaded");
              }}
            />
          </div>
          {matching ? (
            <form className="flex flex-wrap items-center gap-2" onSubmit={match}>
              <input
                className="w-40"
                inputMode="numeric"
                placeholder="TMDB id"
                aria-label="TMDB id"
                value={tmdb}
                onChange={(e) => setTmdb(e.target.value)}
              />
              <button type="submit" className={secondaryBtn} disabled={busy || !tmdb.trim()}>
                Match
              </button>
            </form>
          ) : null}
          <NoteLine note={note} />
        </div>
      </div>
    </li>
  );
}

export function MediaTitlesPage() {
  const [params, setParams] = useSearchParams();
  const library = params.get("library") ?? "";
  const kindFilter = (params.get("kind") ?? "") as Kind | "";
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [limit, setLimit] = useState(PAGE);

  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const movies = useQuery({ queryKey: ["movies"], queryFn: api.listMovies });
  const series = useQuery({ queryKey: ["series"], queryFn: api.listSeries });

  const libraryNames = useMemo(() => new Map((libs.data ?? []).map((l) => [l.id, l.name])), [libs.data]);
  const nameOf = (id?: string) => (id && libraryNames.get(id)) || "Jellyfin";

  const rows = useMemo(() => {
    const all: Row[] = [
      ...(movies.data ?? []).map((t) => ({ kind: "movie" as const, title: t })),
      ...(series.data ?? []).map((t) => ({ kind: "series" as const, title: t })),
    ];
    const needle = q.trim().toLowerCase();
    return all
      .filter((r) => !kindFilter || r.kind === kindFilter)
      .filter((r) => !library || r.title.library_id === library)
      .filter((r) => filter === "all" || r.title.unmatched)
      .filter((r) => !needle || r.title.title.toLowerCase().includes(needle))
      .sort((a, b) => a.title.title.localeCompare(b.title.title));
  }, [movies.data, series.data, q, kindFilter, library, filter]);

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
    setLimit(PAGE);
  };

  const loading = movies.isLoading || series.isLoading;
  const error = movies.error ?? series.error;

  return (
    <div className="space-y-4">
      <PageHeader title="Titles" description="Every movie and series ViewDock knows about. Fix matches, replace posters and set ratings." />
      <div className="flex flex-wrap gap-2">
        <input
          type="search"
          className="min-w-48 flex-1"
          placeholder="Search titles"
          aria-label="Search titles"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setLimit(PAGE);
          }}
        />
        <select aria-label="Type" value={kindFilter} onChange={(e) => setParam("kind", e.target.value)}>
          <option value="">Movies and series</option>
          <option value="movie">Movies</option>
          <option value="series">Series</option>
        </select>
        <select aria-label="Library" value={library} onChange={(e) => setParam("library", e.target.value)}>
          <option value="">All libraries</option>
          {(libs.data ?? []).map((l) => (
            <option key={l.id} value={l.id}>
              {l.name}
            </option>
          ))}
        </select>
        <select aria-label="Match status" value={filter} onChange={(e) => setFilter(e.target.value as Filter)}>
          <option value="all">Any match status</option>
          <option value="unmatched">Unmatched only</option>
        </select>
      </div>
      {loading ? <p className="text-sm text-dim">Loading titles…</p> : null}
      {error ? <p className="text-sm text-danger">{errText(error, "titles could not be loaded")}</p> : null}
      {!loading && !error ? (
        <p className="text-xs text-dim">
          {rows.length} {rows.length === 1 ? "title" : "titles"}
        </p>
      ) : null}
      <ul className="grid gap-3 lg:grid-cols-2">
        {rows.slice(0, limit).map((r) => (
          <TitleRow key={`${r.kind}-${r.title.id}`} row={r} libraryName={nameOf(r.title.library_id)} />
        ))}
      </ul>
      {rows.length > limit ? (
        <button type="button" className={secondaryBtn} onClick={() => setLimit((n) => n + PAGE)}>
          Show more ({rows.length - limit} left)
        </button>
      ) : null}
    </div>
  );
}
