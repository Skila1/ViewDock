import { Link } from "react-router";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";
import { filenameTitle } from "@/lib/format";
import type { BrowseItem, BrowseLayout } from "@/lib/browse";
import type { BrowseSignals } from "@/types/api.gen";

function typeLabel(it: BrowseItem) {
  if (it.anime) return "Anime";
  return it.kind === "movie" ? "Movie" : "TV show";
}

export function BrowseResults({ items, layout, signals }: { items: BrowseItem[]; layout: BrowseLayout; signals?: BrowseSignals }) {
  if (layout === "grid") {
    return (
      <PosterGrid>
        {items.map((it) => (
          <PosterCard key={it.key} to={it.href} title={it.title} posterUrl={it.posterUrl} unmatched={it.unmatched} />
        ))}
      </PosterGrid>
    );
  }
  const watched = new Set(signals?.watched ?? []);
  return (
    <ul className="divide-y divide-line rounded-lg border border-line">
      {items.map((it) => {
        const views = signals?.views[it.key] ?? 0;
        return (
          <li key={it.key}>
            <Link to={it.href} className="flex gap-3 p-2 hover:bg-overlay">
              <div className="h-24 w-16 shrink-0 overflow-hidden rounded bg-raised">
                {it.posterUrl ? <img src={it.posterUrl} alt="" loading="lazy" className="h-full w-full object-cover" /> : null}
              </div>
              <div className="min-w-0 flex-1 space-y-1 py-0.5">
                <p className="flex flex-wrap items-baseline gap-x-2 text-sm">
                  <span className="truncate font-medium text-ink">{filenameTitle(it.title)}</span>
                  {it.year ? <span className="text-xs text-dim">{it.year}</span> : null}
                </p>
                <p className="text-xs text-dim">
                  {[typeLabel(it), ...it.genres.slice(0, 3)].join(", ")}
                  {views ? `. Watched by ${views} ${views === 1 ? "person" : "people"}` : ""}
                  {watched.has(it.key) ? ". You started this" : ""}
                </p>
                {it.overview ? <p className="line-clamp-2 text-xs text-dim">{it.overview}</p> : null}
              </div>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
