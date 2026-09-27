import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { LayoutGrid, List } from "lucide-react";
import { api } from "@/api/api";
import { BrowseFilters } from "@/components/browse/BrowseFilters";
import { BrowseResults } from "@/components/browse/BrowseResults";
import { useBrowseData, useBrowseLayout, useBrowseQuery } from "@/components/browse/useBrowse";
import { ContinueStrip } from "@/components/layout/ContinueStrip";
import { applyBrowse, isFiltered, KIND_OPTIONS, TAG_OPTIONS } from "@/lib/browse";
import { cn } from "@/lib/cn";

export function HomePage() {
  const { items, genres, signals, loading, error } = useBrowseData();
  const [query, setQuery] = useBrowseQuery();
  const [layout, setLayout] = useBrowseLayout();
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
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
      className={cn("tap flex w-9 items-center justify-center rounded-md", layout === value ? "bg-overlay text-ink" : "text-dim hover:text-ink")}
    >
      <Icon className="h-4 w-4" />
    </button>
  );

  return (
    <div>
      {!filtered ? <ContinueStrip items={cont.data ?? []} /> : null}

      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <BrowseFilters query={query} genres={genres} onChange={setQuery} showSort />
        <div role="group" aria-label="Layout" className="flex shrink-0 gap-1 rounded-lg border border-line p-0.5">
          {layoutBtn("grid", "Grid", LayoutGrid)}
          {layoutBtn("list", "Details", List)}
        </div>
      </div>

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
