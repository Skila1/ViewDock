import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { LayoutGrid, List, SlidersHorizontal } from "lucide-react";
import { api } from "@/api/api";
import { BrowseToolbar } from "@/components/browse/BrowseFilters";
import { BrowseResults } from "@/components/browse/BrowseResults";
import { useBrowseData, useBrowseLayout, useBrowseQuery } from "@/components/browse/useBrowse";
import { CustomizeHome } from "@/components/home/CustomizeHome";
import { LibraryTile, PosterTile, WideCard } from "@/components/home/HomeCards";
import { MediaRail, RailItem } from "@/components/home/MediaRail";
import { TitleMenu } from "@/components/media/TitleMenu";
import { applyBrowse, browseParams, isFiltered, KIND_OPTIONS, TAG_OPTIONS, type BrowseItem, type BrowseKind, type BrowseSort } from "@/lib/browse";
import { cn } from "@/lib/cn";
import { continueLabels, homeSections, playHref, progressOf, wideArt } from "@/lib/home";
import { becauseLiked, genreAffinity, homeRows, inGenre, mergeContinue, recentlyReleased, recommendedItems } from "@/lib/homeRows";

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
const sectionGenreHref = (genre: string) => `/?${browseParams({ q: "", kind: "", genre, tag: "", sort: "" }).toString()}`;

function subtitleOf(it: BrowseItem) {
  return it.year ? String(it.year) : it.kind === "series" ? "TV show" : "";
}

function HomeRails() {
  const { items, signals, loading, error } = useBrowseData();
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
  const next = useQuery({ queryKey: ["next-up"], queryFn: api.nextUp, staleTime: 30_000 });
  const prefs = useQuery({ queryKey: ["prefs"], queryFn: api.getPreferences, staleTime: 60_000 });
  const watchlist = useQuery({ queryKey: ["watchlist"], queryFn: api.watchlist, staleTime: 30_000 });
  const [customize, setCustomize] = useState(false);
  const sections = useMemo(() => homeSections(items), [items]);
  const allArt = useMemo(() => items.find((it) => it.backdropUrl)?.backdropUrl ?? items.find((it) => it.posterUrl)?.posterUrl ?? null, [items]);
  const rows = homeRows(prefs.data?.home_rows);
  const continueEntries = useMemo(() => mergeContinue(cont.data ?? [], rows.includes("next_up") ? [] : (next.data ?? [])), [cont.data, next.data, rows]);
  const byKey = useMemo(() => new Map(items.map((it) => [it.key, it])), [items]);

  if (loading) return <p className="text-xs text-dim">Loading…</p>;
  if (error) return <p className="text-xs text-danger">The catalogue could not be loaded.</p>;
  if (!items.length) return <p className="text-sm text-dim">No titles yet. Add a library under Admin, Media.</p>;

  const posterRail = (key: string, title: string, list: BrowseItem[], explain?: string, to?: string) =>
    list.length ? (
      <MediaRail key={key} title={title} subtitle={explain} to={to}>
        {list.map((it) => (
          <RailItem key={it.key} shape="poster">
            <TitleMenu target={{ kind: it.kind, id: it.id, title: it.title }}>
              <PosterTile to={it.href} title={it.title} subtitle={subtitleOf(it)} posterUrl={it.posterUrl} />
            </TitleMenu>
          </RailItem>
        ))}
      </MediaRail>
    ) : null;

  const render = (id: string): ReactNode => {
    if (id === "my_media") {
      return (
        <MediaRail key={id} title="My Media">
          {sections.map((s) => (
            <RailItem key={s.kind} shape="wide">
              <LibraryTile to={sectionHref(s.kind)} label={s.label} count={s.count} art={s.art} artIsPoster={s.artIsPoster} />
            </RailItem>
          ))}
          <RailItem shape="wide">
            <LibraryTile to="/?all=1" label="All titles" count={items.length} art={allArt} />
          </RailItem>
        </MediaRail>
      );
    }
    if (id === "continue") {
      if (!continueEntries.length) return null;
      return (
        <MediaRail key={id} title="Continue Watching">
          {continueEntries.map((e) => {
            if (e.kind === "next") {
              const card = e.card;
              return (
                <RailItem key={`next-${card.id}`} shape="wide">
                  <TitleMenu target={{ kind: "episode", id: card.id, seriesId: card.series_id, title: card.title }}>
                    <WideCard to={playHref("episode", card.id)} title={card.title} subtitle={`Next: ${card.subtitle ?? ""}`} art={wideArt(card)} />
                  </TitleMenu>
                </RailItem>
              );
            }
            const rec = e.rec;
            const { title, subtitle } = continueLabels(rec);
            return (
              <RailItem key={`${rec.item_kind}-${rec.item_id}`} shape="wide">
                <TitleMenu target={{ kind: rec.item_kind === "episode" ? "episode" : "movie", id: rec.item_id, seriesId: rec.card?.series_id, title }}>
                  <WideCard
                    to={playHref(rec.item_kind, rec.item_id, rec.resume_ms ?? rec.position_ms)}
                    title={title}
                    subtitle={subtitle}
                    art={wideArt(rec.card, rec.poster_url)}
                    progress={progressOf(rec)}
                  />
                </TitleMenu>
              </RailItem>
            );
          })}
        </MediaRail>
      );
    }
    if (id === "next_up") {
      const nextUp = next.data ?? [];
      if (!nextUp.length) return null;
      return (
        <MediaRail key={id} title="Next Up">
          {nextUp.map((card) => (
            <RailItem key={card.id} shape="wide">
              <TitleMenu target={{ kind: "episode", id: card.id, seriesId: card.series_id, title: card.title }}>
                <WideCard to={playHref("episode", card.id)} title={card.title} subtitle={card.subtitle} art={wideArt(card)} />
              </TitleMenu>
            </RailItem>
          ))}
        </MediaRail>
      );
    }
    if (id === "my_list") {
      const list = (watchlist.data ?? []).map((e) => byKey.get(`${e.kind}:${e.id}`)).filter((it): it is BrowseItem => Boolean(it));
      return posterRail(id, "My List", list, undefined, "/my-list");
    }
    if (id === "recommended") {
      return posterRail(id, "Recommended for you", recommendedItems(items, signals), "From what you watch, like and dislike");
    }
    if (id === "because_liked") {
      return becauseLiked(items, signals).map((row) => posterRail(`${id}:${row.source.key}`, `Because you liked ${row.source.title}`, row.items));
    }
    if (id === "genres") {
      return genreAffinity(items, signals)
        .slice(0, 2)
        .map((g) => posterRail(`${id}:${g.genre}`, `More ${g.genre}`, inGenre(items, g.genre, signals), "A genre you watch and like", sectionGenreHref(g.genre)));
    }
    if (id === "released") {
      return posterRail(id, "Recently Released", recentlyReleased(items));
    }
    if (id.startsWith("recent:")) {
      const s = sections.find((x) => `recent:${x.kind}` === id);
      return s ? posterRail(id, `Recently Added in ${s.label}`, s.recent, undefined, sectionHref(s.kind, "added")) : null;
    }
    return null;
  };

  return (
    <div>
      <div className="mb-2 flex justify-end">
        <button type="button" onClick={() => setCustomize(true)} className="inline-flex items-center gap-1.5 text-xs text-dim hover:text-ink">
          <SlidersHorizontal className="h-3.5 w-3.5" /> Customize home
        </button>
      </div>
      {rows.map((id) => render(id))}
      {customize ? <CustomizeHome current={prefs.data?.home_rows} onClose={() => setCustomize(false)} /> : null}
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
