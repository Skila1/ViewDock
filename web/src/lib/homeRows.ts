import type { BrowseSignals, HomeCard, ProgressRecord } from "@/types/api.gen";
import { relatedTitles, type BrowseItem } from "./browse";
import { HOME_SECTIONS } from "./home";

/** A home row a profile can show, hide and reorder. */
export type HomeRowDef = { id: string; label: string };

export const HOME_ROWS: HomeRowDef[] = [
  { id: "my_media", label: "My Media" },
  { id: "continue", label: "Continue Watching" },
  { id: "my_list", label: "My List" },
  { id: "recommended", label: "Recommended for you" },
  { id: "because_liked", label: "Because you liked…" },
  { id: "genres", label: "From genres you watch" },
  ...HOME_SECTIONS.map((s) => ({ id: `recent:${s.kind}`, label: `Recently Added in ${s.label}` })),
  { id: "released", label: "Recently Released" },
  { id: "next_up", label: "Next Up (separate row)" },
];

/** Shown when a profile has not chosen its rows. Next Up is part of Continue Watching. */
export const DEFAULT_ROWS = HOME_ROWS.filter((r) => r.id !== "next_up").map((r) => r.id);

/** The profile's rows in order, dropping ids this version does not know. */
export function homeRows(chosen: string[] | undefined): string[] {
  const known = new Set(HOME_ROWS.map((r) => r.id));
  const rows = (chosen ?? []).filter((id) => known.has(id));
  return rows.length ? rows : DEFAULT_ROWS;
}

/** A continue entry: in-progress playback, or the next episode of a show after the last one finished. */
export type ContinueEntry = { kind: "progress"; rec: ProgressRecord } | { kind: "next"; card: HomeCard };

/**
 * mergeContinue promotes each show's next episode into Continue Watching
 * after the in-progress titles, skipping shows that already have an
 * episode in progress.
 */
export function mergeContinue(progress: ProgressRecord[], nextUp: HomeCard[]): ContinueEntry[] {
  const out: ContinueEntry[] = progress.map((rec) => ({ kind: "progress", rec }));
  const busy = new Set(progress.map((p) => p.card?.series_id).filter(Boolean));
  for (const card of nextUp) {
    if (card.series_id && busy.has(card.series_id)) continue;
    out.push({ kind: "next", card });
  }
  return out;
}

type Signals = Pick<BrowseSignals, "watched"> & Partial<BrowseSignals>;

function excluded(signals: Signals | undefined): Set<string> {
  return new Set([...(signals?.watched ?? []), ...(signals?.disliked ?? []), ...(signals?.not_interested ?? [])]);
}

/** Genres weighted by what the profile watched (1), liked (3) and disliked (-3), strongest first. */
export function genreAffinity(items: BrowseItem[], signals: Signals | undefined): { genre: string; weight: number }[] {
  const watched = new Set(signals?.watched ?? []);
  const liked = new Set(signals?.liked ?? []);
  const disliked = new Set(signals?.disliked ?? []);
  const weight = new Map<string, { genre: string; weight: number }>();
  for (const it of items) {
    const w = (watched.has(it.key) ? 1 : 0) + (liked.has(it.key) ? 3 : 0) - (disliked.has(it.key) ? 3 : 0);
    if (!w) continue;
    for (const g of it.genres) {
      const k = g.toLowerCase();
      const cur = weight.get(k) ?? { genre: g, weight: 0 };
      cur.weight += w;
      weight.set(k, cur);
    }
  }
  return [...weight.values()].filter((g) => g.weight > 0).sort((a, b) => b.weight - a.weight || a.genre.localeCompare(b.genre));
}

/** "Because you liked X" rows: titles related to up to `rows` liked titles, minus what the profile saw or rejected. */
export function becauseLiked(items: BrowseItem[], signals: Signals | undefined, rows = 2): { source: BrowseItem; items: BrowseItem[] }[] {
  const skip = excluded(signals);
  const byKey = new Map(items.map((it) => [it.key, it]));
  const out: { source: BrowseItem; items: BrowseItem[] }[] = [];
  for (const key of signals?.liked ?? []) {
    if (out.length === rows) break;
    const source = byKey.get(key);
    if (!source) continue;
    const related = relatedTitles(items, key, 24).filter((it) => !skip.has(it.key) && !(signals?.liked ?? []).includes(it.key));
    if (related.length >= 2) out.push({ source, items: related });
  }
  return out;
}

/** Unwatched titles in a genre, newest first. */
export function inGenre(items: BrowseItem[], genre: string, signals: Signals | undefined, limit = 24): BrowseItem[] {
  const skip = excluded(signals);
  const g = genre.toLowerCase();
  return items
    .filter((it) => !skip.has(it.key) && it.genres.some((x) => x.toLowerCase() === g))
    .sort((a, b) => (b.year ?? 0) - (a.year ?? 0) || b.addedAt.localeCompare(a.addedAt))
    .slice(0, limit);
}

/** Titles by release year, newest first. */
export function recentlyReleased(items: BrowseItem[], limit = 24): BrowseItem[] {
  return items
    .filter((it) => it.year)
    .sort((a, b) => (b.year ?? 0) - (a.year ?? 0) || b.addedAt.localeCompare(a.addedAt))
    .slice(0, limit);
}

/** The server's recommendations as catalogue items, minus anything the profile rejected, liked or listed since. */
export function recommendedItems(items: BrowseItem[], signals: Signals | undefined): BrowseItem[] {
  const skip = new Set([...excluded(signals), ...(signals?.liked ?? []), ...(signals?.watchlist ?? [])]);
  const byKey = new Map(items.map((it) => [it.key, it]));
  return (signals?.recommended ?? []).map((k) => byKey.get(k)).filter((it): it is BrowseItem => Boolean(it) && !skip.has(it!.key));
}
