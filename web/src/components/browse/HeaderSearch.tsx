import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { Search, SlidersHorizontal, X } from "lucide-react";
import { filenameTitle } from "@/lib/format";
import { applyBrowse } from "@/lib/browse";
import { cn } from "@/lib/cn";
import { BrowseFilters } from "./BrowseFilters";
import { useBrowseData, useBrowseQuery } from "./useBrowse";

const SUGGESTIONS = 6;

/**
 * Searches every title the viewer can see, local and Jellyfin. On the home
 * page typing filters the results live; elsewhere it suggests titles and
 * Enter opens the full results on the home page.
 */
export function HeaderSearch() {
  const { pathname } = useLocation();
  const onHome = pathname === "/";
  const [query, setQuery] = useBrowseQuery();
  const { items, genres, signals } = useBrowseData();
  const [text, setText] = useState(query.q);
  const [focused, setFocused] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (onHome) setText(query.q);
  }, [onHome, query.q]);

  useEffect(() => {
    if (!onHome || text === query.q) return;
    const t = window.setTimeout(() => setQuery({ q: text }), 150);
    return () => window.clearTimeout(t);
  }, [onHome, text, query.q, setQuery]);

  useEffect(() => {
    if (!filtersOpen && !focused) return;
    const close = (e: PointerEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) {
        setFiltersOpen(false);
        setFocused(false);
      }
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [filtersOpen, focused]);

  const suggestions = useMemo(
    () => (!onHome && text.trim() ? applyBrowse(items, { ...query, q: text }, signals).slice(0, SUGGESTIONS) : []),
    [onHome, text, items, query, signals],
  );

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setFocused(false);
    setQuery({ q: text });
  };

  const active = [query.kind, query.genre, query.tag].filter(Boolean).length;

  return (
    <div ref={root} className="relative min-w-0 flex-1 max-w-xl">
      <form onSubmit={submit} className="flex items-center gap-2 rounded-lg border border-line bg-raised/60 pl-3 pr-1">
        <Search size={16} className="shrink-0 text-dim" />
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onFocus={() => setFocused(true)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setFocused(false);
              setFiltersOpen(false);
            }
          }}
          placeholder="Search movies, TV and anime"
          aria-label="Search"
          enterKeyHint="search"
          className="h-10 w-full min-w-0 border-0 bg-transparent px-0"
        />
        {text ? (
          <button
            type="button"
            aria-label="Clear search"
            className="tap flex w-8 items-center justify-center text-dim hover:text-ink"
            onClick={() => {
              setText("");
              if (onHome) setQuery({ q: "" });
            }}
          >
            <X className="h-4 w-4" />
          </button>
        ) : null}
        <button
          type="button"
          aria-label="Search filters"
          aria-expanded={filtersOpen}
          className={cn("tap relative flex w-9 items-center justify-center rounded-md", filtersOpen || active ? "text-accent" : "text-dim hover:text-ink")}
          onClick={() => setFiltersOpen((v) => !v)}
        >
          <SlidersHorizontal className="h-4 w-4" />
          {active ? <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-accent" /> : null}
        </button>
      </form>

      {filtersOpen ? (
        <div className="absolute left-0 right-0 top-full z-40 mt-1 space-y-2 rounded-lg border border-line bg-raised p-3 shadow-xl">
          <p className="text-[11px] font-semibold uppercase tracking-wide text-dim">Filter results</p>
          <BrowseFilters query={query} genres={genres} onChange={(patch) => setQuery({ ...patch, q: text })} showSort />
        </div>
      ) : null}

      {focused && !filtersOpen && suggestions.length ? (
        <ul className="absolute left-0 right-0 top-full z-40 mt-1 overflow-hidden rounded-lg border border-line bg-raised shadow-xl">
          {suggestions.map((it) => (
            <li key={it.key}>
              <Link to={it.href} className="flex items-center gap-3 px-3 py-2 hover:bg-overlay" onClick={() => setFocused(false)}>
                <div className="h-12 w-8 shrink-0 overflow-hidden rounded bg-overlay">
                  {it.posterUrl ? <img src={it.posterUrl} alt="" className="h-full w-full object-cover" /> : null}
                </div>
                <div className="min-w-0">
                  <p className="truncate text-sm text-ink">{filenameTitle(it.title)}</p>
                  <p className="text-xs text-dim">
                    {[it.anime ? "Anime" : it.kind === "movie" ? "Movie" : "TV show", it.year].filter(Boolean).join(", ")}
                  </p>
                </div>
              </Link>
            </li>
          ))}
          <li>
            <button type="button" className="w-full px-3 py-2 text-left text-xs text-accent hover:bg-overlay" onClick={() => setQuery({ q: text })}>
              See all results
            </button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}
