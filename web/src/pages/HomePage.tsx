import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { ContinueStrip } from "@/components/layout/ContinueStrip";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";

export function HomePage({ filter }: { filter?: "movies" | "tv" }) {
  const movies = useQuery({ queryKey: ["movies"], queryFn: api.listMovies });
  const series = useQuery({ queryKey: ["series"], queryFn: api.listSeries });
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
  const showMovies = filter !== "tv";
  const showTv = filter !== "movies";

  return (
    <div>
      {!filter ? <ContinueStrip items={cont.data ?? []} /> : null}

      {showMovies ? (
      <section className="mb-6">
        <h2 className="mb-2 text-[13px] font-medium text-dim">Movies</h2>
        {movies.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
        <PosterGrid>
          {(movies.data ?? []).map((m) => (
            <PosterCard
              key={m.id}
              to={`/movies/${m.id}`}
              title={m.title}
              posterUrl={m.poster_url}
              unmatched={m.unmatched}
            />
          ))}
        </PosterGrid>
        {movies.data && movies.data.length === 0 ? (
          <p className="text-xs text-dim">No movies yet.</p>
        ) : null}
      </section>
      ) : null}

      {showTv ? (
      <section>
        <h2 className="mb-2 text-[13px] font-medium text-dim">TV</h2>
        {series.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
        <PosterGrid>
          {(series.data ?? []).map((s) => (
            <PosterCard
              key={s.id}
              to={`/tv/${s.id}`}
              title={s.title}
              posterUrl={s.poster_url}
              unmatched={s.unmatched}
            />
          ))}
        </PosterGrid>
        {series.data && series.data.length === 0 ? (
          <p className="text-xs text-dim">No series yet.</p>
        ) : null}
      </section>
      ) : null}
    </div>
  );
}
