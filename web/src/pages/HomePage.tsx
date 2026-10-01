import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { LayoutGrid, List } from "lucide-react";
import { api } from "@/api/api";
import { BrowseToolbar } from "@/components/browse/BrowseFilters";
import { BrowseResults } from "@/components/browse/BrowseResults";
import { useBrowseData, useBrowseLayout, useBrowseQuery } from "@/components/browse/useBrowse";
import { LibraryTile, PosterTile, WideCard } from "@/components/home/HomeCards";
import { MediaRail, RailItem } from "@/components/home/MediaRail";
import { applyBrowse, browseParams, isFiltered, KIND_OPTIONS, TAG_OPTIONS, type BrowseItem, type BrowseKind, type BrowseSort } from "@/lib/browse";
import { cn } from "@/lib/cn";
import { continueLabels, homeSections, playHref, progressOf, wideArt } from "@/lib/home";

/**
 * The home page. With no search or filter it is a set of rails: the library
 * sections, Continue Watching, Next Up and what was added recently to each
 * section. Searching, filtering or opening "All titles" shows the full grid.
 */
export function HomePage() {
  const [query] = useBrowseQuery();
  return isFiltered(query) || query.all ? <BrowseView /> : <HomeRails />;
}

const sectionHref = (kind: BrowseKind, sort: BrowseSort = "") => `/?${browseParams({ q: "", kind, genre: "", tag: "", sort }).toString()}`;

function subtitleOf(it: BrowseItem) {
  return it.year ? String(it.year) : it.kind === "series" ? "TV show" : "";
}

function HomeRails() {
  const { items, loading, error } = useBrowseData();
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
  const next = useQuery({ queryKey: ["next-up"], queryFn: api.nextUp, staleTime: 30_000 });
  const sections = useMemo(() => homeSections(items), [items]);
  const allArt = useMemo(() => items.find((it) => it.backdropUrl)?.backdropUrl ?? items.find((it) => it.posterUrl)?.posterUrl ?? null, [items]);

  if (loading) return <p className="text-xs text-dim">Loading…</p>;
  if (error) return <p className="text-xs text-danger">The catalogue could not be loaded.</p>;
  if (!items.length) return <p className="text-sm text-dim">No titles yet. Add a library under Admin, Media.</p>;

  const continueItems = cont.data ?? [];
  const nextUp = next.data ?? [];

  return (
    <div>
      <MediaRail title="My Media">
        {sections.map((s) => (
          <RailItem key={s.kind} shape="wide">
            <LibraryTile to={sectionHref(s.kind)} label={s.label} count={s.count} art={s.art} artIsPoster={s.artIsPoster} />
          </RailItem>
        ))}
        <RailItem shape="wide">
          <LibraryTile to="/?all=1" label="All titles" count={items.length} art={allArt} />
        </RailItem>
      </MediaRail>

      {continueItems.length ? (
        <MediaRail title="Continue Watching">
          {continueItems.map((rec) => {
            const { title, subtitle } = continueLabels(rec);
            return (
              <RailItem key={`${rec.item_kind}-${rec.item_id}`} shape="wide">
                <WideCard
                  to={playHref(rec.item_kind, rec.item_id, rec.resume_ms ?? rec.position_ms)}
                  title={title}
                  subtitle={subtitle}
                  art={wideArt(rec.card, rec.poster_url)}
                  progress={progressOf(rec)}
                />
              </RailItem>
            );
          })}
        </MediaRail>
      ) : null}

      {nextUp.length ? (
        <MediaRail title="Next Up">
          {nextUp.map((card) => (
            <RailItem key={card.id} shape="wide">
              <WideCard to={playHref("episode", card.id)} title={card.title} subtitle={card.subtitle} art={wideArt(card)} />
            </RailItem>
          ))}
        </MediaRail>
      ) : null}

      {sections.map((s) => (
        <MediaRail key={s.kind} title={`Recently Added in ${s.label}`} to={sectionHref(s.kind, "added")}>
          {s.recent.map((it) => (
            <RailItem key={it.key} shape="poster">
              <PosterTile to={it.href} title={it.title} subtitle={subtitleOf(it)} posterUrl={it.posterUrl} />
            </RailItem>
          ))}
        </MediaRail>
      ))}
    </div>
  );
}

function BrowseView() {
  const { items, genres, signals, loading, error } = useBrowseData();
  const [query, setQuery] = useBrowseQuery();
  const [layout, setLayout] = useBrowseLayout();
  const results = useMemo(() => applyBrowse(items, query, signals), [items, query, signals]);
  const filtered = isFiltered(query);

  const heading = useMemo(() => {
    const kind = KIND_OPTIONS.find((o) => o.value === query.kind && o.value)?.label;
    const tag = TAG_OPTIONS.find((o) => o.value === query.tag)?.label;
    const parts = [tag, query.genre, kind ?? (filtered ? "Titles" : "Everything")].filter(Boolean);
    return query.q.trim() ? `Results for "${query.q.trim()}"` : parts.join(" ");
  }, [filtered, query]);

  const layoutBtn = (value: typeof layout, label: string, Icon: typeof List) => (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={layout === value}
      onClick={() => setLayout(value)}
      className={cn("flex h-9 w-9 items-center justify-center rounded-lg", layout === value ? "bg-overlay text-ink" : "text-dim hover:text-ink")}
    >
      <Icon className="h-4 w-4" />
    </button>
  );

  return (
    <div>
      <BrowseToolbar
        className="mb-3"
        query={query}
        genres={genres}
        onChange={setQuery}
        trailing={
          <div role="group" aria-label="Layout" className="flex shrink-0 gap-0.5">
            {layoutBtn("grid", "Grid", LayoutGrid)}
            {layoutBtn("list", "Details", List)}
          </div>
        }
      />

      <div className="mb-2 flex flex-wrap items-baseline gap-2">
        <h2 className="text-[13px] font-medium text-dim">{heading}</h2>
        {!loading ? <span className="text-xs text-dim">{results.length}</span> : null}
        {filtered ? (
          <button type="button" className="text-xs text-accent" onClick={() => setQuery({ q: "", kind: "", genre: "", tag: "", sort: "" })}>
            Clear filters
          </button>
        ) : null}
      </div>

      {loading ? <p className="text-xs text-dim">Loading…</p> : null}
      {error ? <p className="text-xs text-danger">The catalogue could not be loaded.</p> : null}
      {!loading && !error && results.length === 0 ? (
        <p className="text-xs text-dim">{items.length ? "Nothing matches these filters." : "No titles yet."}</p>
      ) : null}
      <BrowseResults items={results} layout={layout} signals={signals} />
    </div>
  );
}
