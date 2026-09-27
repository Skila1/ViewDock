import { cn } from "@/lib/cn";
import { KIND_OPTIONS, SORT_OPTIONS, TAG_OPTIONS, type BrowseQuery } from "@/lib/browse";

const chip = "tap rounded-full border px-3 text-xs";
const chipOn = "border-accent bg-accent/15 text-ink";
const chipOff = "border-line text-dim hover:text-ink";

type Props = {
  query: BrowseQuery;
  genres: string[];
  onChange: (patch: Partial<BrowseQuery>) => void;
  showSort?: boolean;
  className?: string;
};

/** Media type, genre, tag and sort controls shared by the header search and the home page. */
export function BrowseFilters({ query, genres, onChange, showSort, className }: Props) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      <div role="radiogroup" aria-label="Media type" className="flex flex-wrap gap-1">
        {KIND_OPTIONS.map((o) => (
          <button
            key={o.value || "all"}
            type="button"
            role="radio"
            aria-checked={query.kind === o.value}
            className={cn(chip, query.kind === o.value ? chipOn : chipOff)}
            onClick={() => onChange({ kind: o.value })}
          >
            {o.label}
          </button>
        ))}
      </div>
      <select
        aria-label="Genre"
        className="h-9 text-xs"
        value={query.genre}
        onChange={(e) => onChange({ genre: e.target.value })}
        disabled={!genres.length && !query.genre}
      >
        <option value="">{genres.length ? "All genres" : "No genres yet"}</option>
        {query.genre && !genres.includes(query.genre) ? <option value={query.genre}>{query.genre}</option> : null}
        {genres.map((g) => (
          <option key={g} value={g}>
            {g}
          </option>
        ))}
      </select>
      <div role="group" aria-label="Tags" className="flex flex-wrap gap-1">
        {TAG_OPTIONS.map((o) => (
          <button
            key={o.value}
            type="button"
            aria-pressed={query.tag === o.value}
            className={cn(chip, query.tag === o.value ? chipOn : chipOff)}
            onClick={() => onChange({ tag: query.tag === o.value ? "" : o.value })}
          >
            {o.label}
          </button>
        ))}
      </div>
      {showSort ? (
        <select aria-label="Sort" className="h-9 text-xs" value={query.sort} onChange={(e) => onChange({ sort: e.target.value as BrowseQuery["sort"] })}>
          {SORT_OPTIONS.map((o) => (
            <option key={o.value || "best"} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      ) : null}
    </div>
  );
}
