import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@/api/api";
import { Logo } from "@/components/brand/Logo";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";
import { activityInstanceId } from "@/lib/discordActivity";

const WAIT_POLL_MS = 5_000;

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : "Something went wrong";
}

function useDebounced(value: string, ms: number): string {
  const [out, setOut] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setOut(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return out;
}

// ActivityPage runs inside the Discord Activity. Everyone in the voice
// channel lands in the same watch party; the first person who can start one
// picks the title.
export function ActivityPage() {
  const navigate = useNavigate();
  const instanceId = activityInstanceId();
  const room = useQuery({
    queryKey: ["activity-room", instanceId],
    queryFn: () => api.activityRoom({ instance_id: instanceId }),
    enabled: Boolean(instanceId),
    retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
    refetchInterval: (q) => (q.state.data && !q.state.data.room ? WAIT_POLL_MS : false),
  });
  const create = useMutation({
    mutationFn: (item: { item_kind: "movie" | "episode"; item_id: string }) =>
      api.activityRoom({ instance_id: instanceId, ...item }),
  });

  const code = create.data?.room?.invite_code ?? room.data?.room?.invite_code;
  useEffect(() => {
    if (code) navigate(`/together/${encodeURIComponent(code)}`, { replace: true });
  }, [code, navigate]);

  let body;
  if (!instanceId) {
    body = <p className="text-sm text-danger">Discord did not pass the Activity details. Close the Activity and start it again.</p>;
  } else if (room.isPending || code) {
    body = <p className="text-sm text-dim">Finding this channel's watch party…</p>;
  } else if (room.isError) {
    body = (
      <>
        <p className="text-sm text-danger">{errMessage(room.error)}</p>
        <button type="button" className="tap w-full rounded-full border border-line text-sm" onClick={() => void room.refetch()}>
          Try again
        </button>
      </>
    );
  } else if (room.data?.can_create) {
    body = (
      <TitlePicker
        busy={create.isPending}
        error={create.isError ? errMessage(create.error) : ""}
        onPick={(item) => create.mutate(item)}
      />
    );
  } else {
    body = (
      <p className="text-sm text-dim">
        Nobody has started a watch party in this channel yet. It opens here as soon as someone picks a title.
      </p>
    );
  }

  return (
    <div className="min-h-dvh bg-bg p-4 pt-[max(1rem,var(--sat))]">
      <div className="mx-auto w-full max-w-6xl space-y-4">
        <div className="flex items-center gap-3">
          <Logo className="h-9 w-9" />
          <h1 className="text-lg font-semibold tracking-tight">Watch together</h1>
        </div>
        {body}
      </div>
    </div>
  );
}

type Item = { item_kind: "movie" | "episode"; item_id: string };
type Picked = { id: string; title: string };

function TitlePicker({ busy, error, onPick }: { busy: boolean; error: string; onPick: (item: Item) => void }) {
  const [q, setQ] = useState("");
  const [series, setSeries] = useState<Picked | null>(null);
  const term = useDebounced(q.trim(), 300);
  const searching = term.length >= 2;
  const movies = useQuery({ queryKey: ["movies"], queryFn: api.listMovies, enabled: !searching && !series });
  const shows = useQuery({ queryKey: ["series"], queryFn: api.listSeries, enabled: !searching && !series });
  const results = useQuery({
    queryKey: ["activity-search", term],
    queryFn: () => api.search(term),
    enabled: searching && !series,
  });
  const detail = useQuery({
    queryKey: ["series", series?.id],
    queryFn: () => api.getSeries(series!.id),
    enabled: Boolean(series),
  });

  if (series) {
    return (
      <div className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-sm font-medium">{series.title}</p>
          <button type="button" className="text-xs text-dim hover:text-ink" onClick={() => setSeries(null)}>
            Back
          </button>
        </div>
        {error ? <p className="text-xs text-danger">{error}</p> : null}
        {detail.isPending ? <p className="text-sm text-dim">Loading episodes…</p> : null}
        {detail.isError ? <p className="text-sm text-danger">{errMessage(detail.error)}</p> : null}
        <div className="space-y-3">
          {(detail.data?.seasons ?? []).map((season) => (
            <div key={season.id ?? season.number}>
              <p className="mb-1 text-xs font-medium text-dim">{season.title || `Season ${season.number}`}</p>
              <ul className="space-y-1">
                {season.episodes.map((ep) => (
                  <li key={ep.id}>
                    <button
                      type="button"
                      disabled={busy}
                      className="w-full truncate rounded-md px-2 py-1.5 text-left text-sm hover:bg-overlay disabled:opacity-50"
                      onClick={() => onPick({ item_kind: "episode", item_id: ep.id })}
                    >
                      {ep.number}. {ep.title}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
    );
  }

  const hits = results.data?.items ?? [];
  const pickMovie = (id: string) => onPick({ item_kind: "movie", item_id: id });
  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <p className="text-sm text-dim">Pick something for everyone in this channel to watch.</p>
        <input
          type="search"
          className="w-full max-w-md"
          placeholder="Search movies and TV"
          aria-label="Search movies and TV"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {error ? <p className="text-xs text-danger">{error}</p> : null}
      </div>

      {searching ? (
        <section>
          {results.isPending ? <p className="text-xs text-dim">Searching…</p> : null}
          {results.isError ? <p className="text-xs text-danger">{errMessage(results.error)}</p> : null}
          {results.isSuccess && hits.length === 0 ? <p className="text-xs text-dim">No matches.</p> : null}
          <PosterGrid>
            {hits.map((hit) => (
              <PosterCard
                key={`${hit.item_kind}:${hit.item_id}`}
                title={hit.title}
                posterUrl={hit.poster_url}
                unmatched={hit.unmatched}
                disabled={busy}
                onSelect={() =>
                  hit.item_kind === "series"
                    ? setSeries({ id: hit.item_id, title: hit.title })
                    : onPick({ item_kind: hit.item_kind, item_id: hit.item_id })
                }
              />
            ))}
          </PosterGrid>
        </section>
      ) : (
        <>
          <section>
            <h2 className="mb-2 text-[13px] font-medium text-dim">Movies</h2>
            {movies.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
            {movies.isError ? <p className="text-xs text-danger">{errMessage(movies.error)}</p> : null}
            <PosterGrid>
              {(movies.data ?? []).map((m) => (
                <PosterCard
                  key={m.id}
                  title={m.title}
                  posterUrl={m.poster_url}
                  unmatched={m.unmatched}
                  disabled={busy}
                  onSelect={() => pickMovie(m.id)}
                />
              ))}
            </PosterGrid>
            {movies.data && movies.data.length === 0 ? <p className="text-xs text-dim">No movies yet.</p> : null}
          </section>
          <section>
            <h2 className="mb-2 text-[13px] font-medium text-dim">TV</h2>
            {shows.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
            {shows.isError ? <p className="text-xs text-danger">{errMessage(shows.error)}</p> : null}
            <PosterGrid>
              {(shows.data ?? []).map((s) => (
                <PosterCard
                  key={s.id}
                  title={s.title}
                  posterUrl={s.poster_url}
                  unmatched={s.unmatched}
                  disabled={busy}
                  onSelect={() => setSeries({ id: s.id, title: s.title })}
                />
              ))}
            </PosterGrid>
            {shows.data && shows.data.length === 0 ? <p className="text-xs text-dim">No series yet.</p> : null}
          </section>
        </>
      )}
    </div>
  );
}
