import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@/api/api";
import { Logo } from "@/components/brand/Logo";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";
import { activityInstanceId, forgetLeftParty, leftPartyCode } from "@/lib/discordActivity";

const WAIT_POLL_MS = 5_000;
// While someone watches on their own after leaving, the page still checks
// now and then whether the channel's party is running, for the Rejoin bar.
const LEFT_POLL_MS = 15_000;

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

// ActivityPage runs inside the Discord Activity. The first person in the
// voice channel to open it hosts (an administrator who joins later takes
// over) and picks the title; everyone else in the channel is sent into that
// party. Someone who leaves the party watches on their own from here and can
// rejoin it at any time.
export function ActivityPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const instanceId = activityInstanceId();
  const [left, setLeft] = useState(() => leftPartyCode() || ((location.state as { left?: string } | null)?.left ?? ""));
  const room = useQuery({
    queryKey: ["activity-room", instanceId],
    queryFn: () => api.activityRoom({ instance_id: instanceId }),
    enabled: Boolean(instanceId),
    retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
    refetchInterval: (q) => {
      const d = q.state.data;
      if (!d) return false;
      if (!d.room) return WAIT_POLL_MS;
      return left && d.room.invite_code === left ? LEFT_POLL_MS : false;
    },
  });
  const create = useMutation({
    mutationFn: (item: Item) => api.activityRoom({ instance_id: instanceId, ...item }),
  });

  const existing = room.data?.room ?? null;
  const skipped = Boolean(existing && left && existing.invite_code === left);
  const code = create.data?.room?.invite_code ?? (skipped ? undefined : existing?.invite_code);
  useEffect(() => {
    if (!code) return;
    // A different party than the one left (it ended and a new one started)
    // is joined as usual.
    forgetLeftParty();
    navigate(`/together/${encodeURIComponent(code)}`, { replace: true });
  }, [code, navigate]);

  const rejoin = (invite: string) => {
    forgetLeftParty();
    setLeft("");
    navigate(`/together/${encodeURIComponent(invite)}`, { replace: true });
  };
  const watchAlone = (item: Item) =>
    navigate(`/watch/${item.item_kind}/${encodeURIComponent(item.item_id)}`, { state: { from: "/activity" } });

  const host = room.data?.host ?? null;
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
  } else if (skipped && existing) {
    body = (
      <>
        <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-line bg-raised p-4">
          <p className="min-w-0 flex-1 text-sm">
            <span className="text-dim">The channel's party is still playing: </span>
            <span className="font-medium">{existing.title || "Watch party"}</span>
          </p>
          <button type="button" className="btn-green rounded-full px-4 py-1.5 text-sm" onClick={() => rejoin(existing.invite_code)}>
            Rejoin
          </button>
        </div>
        <TitlePicker
          intro="You left the party. Pick something to watch on your own; you can rejoin the party any time."
          busy={false}
          error=""
          onPick={watchAlone}
        />
      </>
    );
  } else if (room.data?.can_create) {
    body = (
      <TitlePicker
        intro={host?.you ? "You're hosting. Pick something for everyone in this channel to watch." : undefined}
        busy={create.isPending}
        error={create.isError ? errMessage(create.error) : ""}
        onPick={(item) => create.mutate(item)}
      />
    );
  } else {
    body = (
      <p className="text-sm text-dim" role="status">
        {host
          ? `${host.name} is hosting this channel. The party opens here as soon as they pick something to watch.`
          : "Nobody has started a watch party in this channel yet. It opens here as soon as someone picks a title."}
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

function TitlePicker({ intro, busy, error, onPick }: { intro?: string; busy: boolean; error: string; onPick: (item: Item) => void }) {
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
        <p className="text-sm text-dim">{intro ?? "Pick something for everyone in this channel to watch."}</p>
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
