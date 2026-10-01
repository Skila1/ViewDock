import type { HomeCard, ProgressRecord } from "@/types/api.gen";
import type { BrowseItem, BrowseKind } from "./browse";

/** The home page's library sections, in the order they are shown. */
export const HOME_SECTIONS: { kind: Exclude<BrowseKind, "">; label: string }[] = [
  { kind: "movie", label: "Movies" },
  { kind: "series", label: "Shows" },
  { kind: "anime", label: "Anime" },
];

/** Same split as the browse filter: anime is its own section, not a movie or a show. */
export function inSection(it: BrowseItem, kind: Exclude<BrowseKind, "">): boolean {
  if (kind === "anime") return it.anime;
  return it.kind === kind && !it.anime;
}

export type HomeSection = {
  kind: Exclude<BrowseKind, "">;
  label: string;
  count: number;
  /** Wide artwork for the section's tile: a recent title's backdrop, or else its poster. */
  art: string | null;
  artIsPoster: boolean;
  /** Most recently added first. */
  recent: BrowseItem[];
};

const byAdded = (a: BrowseItem, b: BrowseItem) =>
  b.addedAt.localeCompare(a.addedAt) || a.title.localeCompare(b.title, undefined, { sensitivity: "base" });

/** Sections that have at least one title, each with its newest titles. */
export function homeSections(items: BrowseItem[], recentLimit = 24): HomeSection[] {
  const out: HomeSection[] = [];
  for (const s of HOME_SECTIONS) {
    const list = items.filter((it) => inSection(it, s.kind)).sort(byAdded);
    if (!list.length) continue;
    const withBackdrop = list.find((it) => it.backdropUrl);
    const withPoster = list.find((it) => it.posterUrl);
    out.push({
      ...s,
      count: list.length,
      art: withBackdrop?.backdropUrl ?? withPoster?.posterUrl ?? null,
      artIsPoster: !withBackdrop && Boolean(withPoster),
      recent: list.slice(0, recentLimit),
    });
  }
  return out;
}

export type WideArt = { url: string | null; poster: boolean };

/**
 * The best artwork for a wide (16:9) card: an episode still, then the
 * backdrop, then the poster, which the card then shows letterboxed.
 */
export function wideArt(card: HomeCard | undefined, fallbackPoster?: string | null): WideArt {
  const wide = card?.thumb_url || card?.backdrop_url;
  if (wide) return { url: wide, poster: false };
  const poster = card?.poster_url || fallbackPoster || null;
  return { url: poster, poster: Boolean(poster) };
}

/** Fraction watched, 0 to 1. */
export function progressOf(rec: ProgressRecord): number {
  if (!rec.duration_ms) return 0;
  const at = rec.resume_ms ?? rec.position_ms;
  return Math.min(1, Math.max(0, at / rec.duration_ms));
}

/** Where a continue or next-up card plays from. */
export function playHref(kind: string, id: string, resumeMS?: number): string {
  const k = kind === "episode" ? "episode" : "movie";
  return `/watch/${k}/${id}${resumeMS ? `?t=${Math.floor(resumeMS)}` : ""}`;
}

/** Title and line under a continue card; older servers send no card. */
export function continueLabels(rec: ProgressRecord): { title: string; subtitle: string } {
  if (rec.card) return { title: rec.card.title, subtitle: rec.card.subtitle ?? "" };
  return { title: rec.title || "Untitled", subtitle: "" };
}
