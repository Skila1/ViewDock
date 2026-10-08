import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { Search, SlidersHorizontal, X } from "lucide-react";
import { filenameTitle } from "@/lib/format";
import { applyBrowse, type BrowseQuery } from "@/lib/browse";
import { cn } from "@/lib/cn";
import { BrowseFilterPanel } from "./BrowseFilters";
import { useBrowseData } from "./useBrowse";

const SUGGESTIONS = 6;
const EMPTY: BrowseQuery = { q: "", kind: "", genre: "", tag: "", sort: "" };

/**
 * Searches every title the viewer can see, local and Jellyfin. The text and
 * filters here only shape the dropdown results; they never change the home
 * page's own filters or listing.
 */
export function HeaderSearch() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const { items, genres, signals } = useBrowseData();
  const [query, setQuery] = useState<BrowseQuery>(EMPTY);
  const [focused, setFocused] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);

  const patch = (next: Partial<BrowseQuery>) => {
    setQuery((q) => ({ ...q, ...next }));
    setShowAll(false);
  };

  useEffect(() => {
    setFocused(false);
    setFiltersOpen(false);
  }, [pathname]);

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

  const active = [query.kind, query.genre, query.tag, query.sort].filter(Boolean).length;
  const searching = Boolean(query.q.trim()) || active > 0;
  const results = useMemo(() => (searching ? applyBrowse(items, query, signals) : []), [searching, items, query, signals]);
  const shown = showAll ? results : results.slice(0, SUGGESTIONS);
  const open = searching && (focused || filtersOpen);

  const pick = (href: string) => {
    setFocused(false);
    setFiltersOpen(false);
    input.current?.blur();
    navigate(href);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (results[0]) pick(results[0].href);
  };

  return (
    <div ref={root} className="relative min-w-0 flex-1 max-w-xl">
      <form onSubmit={submit} className="flex items-center gap-2 rounded-xl border border-line bg-raised/50 pl-3 pr-1 focus-within:border-accent/60">
        <Search size={16} className="shrink-0 text-dim" />
        <input
          ref={input}
          value={query.q}
          onChange={(e) => patch({ q: e.target.value })}
          onFocus={() => setFocused(true)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setFocused(false);
              setFiltersOpen(false);
            }
          }}
          placeholder="Search movies, TV and anime"
          aria-label="Search"
          aria-expanded={open}
          aria-controls="header-search-results"
          enterKeyHint="search"
          className="h-10 w-full min-w-0 border-0 bg-transparent px-0 focus:outline-none"
        />
        {query.q ? (
          <button
            type="button"
            aria-label="Clear search"
            className="tap flex w-8 items-center justify-center text-dim hover:text-ink"
            onClick={() => {
              patch({ q: "" });
              input.current?.focus();
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

      {filtersOpen || open ? (
        <div className="absolute left-0 top-full z-40 mt-2 w-full min-w-[min(28rem,calc(100vw-1.5rem))] overflow-hidden rounded-xl border border-line bg-raised shadow-2xl">
          {filtersOpen ? (
            <div className={cn("p-3", open && "border-b border-line")}>
              <BrowseFilterPanel query={query} genres={genres} onChange={patch} onReset={() => patch({ kind: "", genre: "", tag: "", sort: "" })} />
            </div>
          ) : null}
          {open ? (
            results.length ? (
              <ul id="header-search-results" className={cn("py-1", showAll && "max-h-[60vh] overflow-y-auto")}>
                {shown.map((it) => (
                  <li key={it.key}>
                    <Link
                      to={it.href}
                      className="flex items-center gap-3 px-3 py-2 hover:bg-overlay focus-visible:bg-overlay focus-visible:outline-none"
                      onClick={() => {
                        setFocused(false);
                        setFiltersOpen(false);
                      }}
                    >
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
                {results.length > SUGGESTIONS ? (
                  <li>
                    <button type="button" className="w-full px-3 py-2 text-left text-xs text-accent hover:bg-overlay" onClick={() => setShowAll((v) => !v)}>
                      {showAll ? "Show fewer" : `Show all ${results.length} results`}
                    </button>
                  </li>
                ) : null}
              </ul>
            ) : (
              <p id="header-search-results" className="px-3 py-3 text-xs text-dim">
                No titles match{query.q.trim() ? ` "${query.q.trim()}"` : ""}.
              </p>
            )
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
