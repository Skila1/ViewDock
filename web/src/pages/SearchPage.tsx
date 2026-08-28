import { useMemo } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";
import type { SearchHit } from "@/types/api.gen";

export function SearchPage() {
  const [params] = useSearchParams();
  const q = params.get("q") ?? "";
  const res = useQuery({
    queryKey: ["search", q],
    queryFn: () => api.search(q),
    enabled: q.length > 0,
  });

  const hits = useMemo<SearchHit[]>(() => {
    const data = res.data;
    if (!data) return [];
    if (data.items?.length) return data.items;
    const out: SearchHit[] = [];
    for (const m of data.movies ?? []) {
      out.push({
        item_kind: "movie",
        item_id: m.id,
        title: m.title,
        year: m.year,
        poster_url: m.poster_url,
        unmatched: m.unmatched,
      });
    }
    for (const s of data.series ?? []) {
      out.push({
        item_kind: "series",
        item_id: s.id,
        title: s.title,
        year: s.year,
        poster_url: s.poster_url,
        unmatched: s.unmatched,
      });
    }
    for (const e of data.episodes ?? []) {
      out.push({
        item_kind: "episode",
        item_id: e.id,
        title: e.title || `S${e.season}E${e.number}`,
        unmatched: e.unmatched,
      });
    }
    return out;
  }, [res.data]);

  const href = (h: SearchHit) => {
    if (h.item_kind === "movie") return `/movies/${h.item_id}`;
    if (h.item_kind === "series") return `/tv/${h.item_id}`;
    return `/watch/episode/${h.item_id}`;
  };

  return (
    <div>
      <h1 className="mb-3 text-sm text-dim">Search {q ? `“${q}”` : ""}</h1>
      {res.isLoading ? <p className="text-xs text-dim">Searching…</p> : null}
      <PosterGrid>
        {hits.map((h) => (
          <PosterCard
            key={`${h.item_kind}-${h.item_id}`}
            to={href(h)}
            title={h.title}
            posterUrl={h.poster_url}
            unmatched={h.unmatched}
          />
        ))}
      </PosterGrid>
      {q && !res.isLoading && hits.length === 0 ? <p className="text-xs text-dim">No matches.</p> : null}
    </div>
  );
}
