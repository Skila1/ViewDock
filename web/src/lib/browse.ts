import type { BrowseSignals, Movie, Series } from "@/types/api.gen";

export type BrowseKind = "" | "movie" | "series" | "anime";
export type BrowseTag = "" | "unwatched" | "most_viewed" | "recommended";
/** "" is the natural order: recommendation rank, view count, or title. */
export type BrowseSort = "" | "title" | "title_desc" | "year" | "added";
export type BrowseLayout = "grid" | "list";

export type BrowseQuery = {
  q: string;
  kind: BrowseKind;
  genre: string;
  tag: BrowseTag;
  sort: BrowseSort;
};

export type BrowseItem = {
  key: string;
  kind: "movie" | "series";
  id: string;
  title: string;
  year: number | null;
  posterUrl: string | null;
  unmatched: boolean;
  overview: string;
  genres: string[];
  anime: boolean;
  addedAt: string;
  href: string;
};

export const KIND_OPTIONS: { value: BrowseKind; label: string }[] = [
  { value: "", label: "All" },
  { value: "movie", label: "Movies" },
  { value: "series", label: "TV shows" },
  { value: "anime", label: "Anime" },
];

export const TAG_OPTIONS: { value: Exclude<BrowseTag, "">; label: string }[] = [
  { value: "recommended", label: "Recommended" },
  { value: "most_viewed", label: "Most viewed" },
  { value: "unwatched", label: "Unwatched" },
];

export const SORT_OPTIONS: { value: BrowseSort; label: string }[] = [
  { value: "", label: "Best match" },
  { value: "title", label: "Title A to Z" },
  { value: "title_desc", label: "Title Z to A" },
  { value: "year", label: "Newest release" },
  { value: "added", label: "Recently added" },
];

const KINDS = new Set<string>(KIND_OPTIONS.map((o) => o.value));
const TAGS = new Set<string>(["", ...TAG_OPTIONS.map((o) => o.value)]);
const SORTS = new Set<string>(SORT_OPTIONS.map((o) => o.value));

export function parseBrowse(params: URLSearchParams): BrowseQuery {
  const pick = <T extends string>(key: string, allowed: Set<string>) => {
    const v = params.get(key) ?? "";
    return (allowed.has(v) ? v : "") as T;
  };
  return {
    q: params.get("q") ?? "",
    kind: pick<BrowseKind>("type", KINDS),
    genre: params.get("genre") ?? "",
    tag: pick<BrowseTag>("tag", TAGS),
    sort: pick<BrowseSort>("sort", SORTS),
  };
}

export function browseParams(query: BrowseQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (query.q.trim()) p.set("q", query.q);
  if (query.kind) p.set("type", query.kind);
  if (query.genre) p.set("genre", query.genre);
  if (query.tag) p.set("tag", query.tag);
  if (query.sort) p.set("sort", query.sort);
  return p;
}

export function isFiltered(query: BrowseQuery): boolean {
  return Boolean(query.q.trim() || query.kind || query.genre || query.tag || query.sort);
}

export function toBrowseItems(movies: Movie[], series: Series[]): BrowseItem[] {
  return [
    ...movies.map((m) => item("movie", m, `/movies/${m.id}`)),
    ...series.map((s) => item("series", s, `/tv/${s.id}`)),
  ];
}

function item(kind: "movie" | "series", t: Movie | Series, href: string): BrowseItem {
  return {
    key: `${kind}:${t.id}`,
    kind,
    id: t.id,
    title: t.title,
    year: t.year,
    posterUrl: t.poster_url,
    unmatched: t.unmatched,
    overview: t.overview ?? "",
    genres: t.genres ?? [],
    anime: Boolean(t.anime),
    addedAt: t.added_at ?? "",
    href,
  };
}

/** Every genre in the catalogue, most common first. */
export function genresOf(items: BrowseItem[]): string[] {
  const count = new Map<string, number>();
  for (const it of items) for (const g of it.genres) count.set(g, (count.get(g) ?? 0) + 1);
  return [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([g]) => g);
}

function normal(s: string): string {
  return s
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase();
}

/** Higher is a better match; 0 means the item does not match the text. */
function matchScore(it: BrowseItem, words: string[]): number {
  if (!words.length) return 1;
  const title = normal(it.title);
  const extra = normal(`${it.year ?? ""} ${it.genres.join(" ")}`);
  let score = 0;
  for (const w of words) {
    if (title.startsWith(w)) score += 3;
    else if (title.split(/\s+/).some((part) => part.startsWith(w))) score += 2;
    else if (title.includes(w)) score += 1;
    else if (extra.includes(w)) score += 0.5;
    else return 0;
  }
  return score;
}

/**
 * Titles most like the one with this key: shared genres count most, then
 * anime versus not, kind, and release year. Titles sharing nothing are left out.
 */
export function relatedTitles(items: BrowseItem[], key: string, limit = 12): BrowseItem[] {
  const self = items.find((it) => it.key === key);
  if (!self) return [];
  const genres = new Set(self.genres.map((g) => g.toLowerCase()));
  const selfTitle = normal(self.title);
  const scored: { it: BrowseItem; score: number }[] = [];
  for (const it of items) {
    if (it.key === key || (normal(it.title) === selfTitle && it.year === self.year)) continue;
    const shared = it.genres.reduce((n, g) => n + (genres.has(g.toLowerCase()) ? 1 : 0), 0);
    if (genres.size > 0 && shared === 0) continue;
    let score = shared * 3;
    if (it.anime === self.anime) score += 2;
    else score -= 5;
    if (it.kind === self.kind) score += 1;
    if (self.year && it.year) {
      const gap = Math.abs(self.year - it.year);
      if (gap <= 3) score += 1.5;
      else if (gap <= 10) score += 0.5;
    }
    if (score > 0) scored.push({ it, score });
  }
  scored.sort((a, b) => b.score - a.score || a.it.title.localeCompare(b.it.title, undefined, { sensitivity: "base" }));
  return scored.slice(0, limit).map((s) => s.it);
}

export function applyBrowse(items: BrowseItem[], query: BrowseQuery, signals?: BrowseSignals): BrowseItem[] {
  const words = normal(query.q).split(/\s+/).filter(Boolean);
  const watched = new Set(signals?.watched ?? []);
  const views = signals?.views ?? {};
  const rank = new Map((signals?.recommended ?? []).map((k, i) => [k, i]));
  const genre = query.genre.toLowerCase();

  const scored = items
    .filter((it) => {
      if (query.kind === "anime") return it.anime;
      if (query.kind === "movie") return it.kind === "movie" && !it.anime;
      if (query.kind === "series") return it.kind === "series" && !it.anime;
      return true;
    })
    .filter((it) => !genre || it.genres.some((g) => g.toLowerCase() === genre))
    .filter((it) => {
      if (query.tag === "unwatched") return !watched.has(it.key);
      if (query.tag === "most_viewed") return (views[it.key] ?? 0) > 0;
      if (query.tag === "recommended") return rank.has(it.key);
      return true;
    })
    .map((it) => ({ it, score: matchScore(it, words) }))
    .filter((s) => s.score > 0);

  const byTitle = (a: BrowseItem, b: BrowseItem) => a.title.localeCompare(b.title, undefined, { sensitivity: "base" });
  scored.sort((x, y) => {
    const a = x.it;
    const b = y.it;
    switch (query.sort) {
      case "title":
        return byTitle(a, b);
      case "title_desc":
        return byTitle(b, a);
      case "year":
        return (b.year ?? 0) - (a.year ?? 0) || byTitle(a, b);
      case "added":
        return b.addedAt.localeCompare(a.addedAt) || byTitle(a, b);
    }
    if (query.tag === "recommended") return (rank.get(a.key) ?? 0) - (rank.get(b.key) ?? 0);
    if (query.tag === "most_viewed") return (views[b.key] ?? 0) - (views[a.key] ?? 0) || byTitle(a, b);
    if (words.length) return y.score - x.score || byTitle(a, b);
    return byTitle(a, b);
  });
  return scored.map((s) => s.it);
}
