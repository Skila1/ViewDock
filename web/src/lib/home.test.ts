import { describe, expect, it } from "vitest";
import type { BrowseItem } from "./browse";
import { continueLabels, homeSections, playHref, progressOf, wideArt } from "./home";

const item = (over: Partial<BrowseItem>): BrowseItem => ({
  key: `movie:${over.id ?? "x"}`,
  kind: "movie",
  id: "x",
  title: "X",
  year: 2020,
  posterUrl: null,
  backdropUrl: null,
  unmatched: false,
  overview: "",
  genres: [],
  anime: false,
  addedAt: "2026-01-01T00:00:00Z",
  href: "/movies/x",
  ...over,
});

describe("homeSections", () => {
  const items = [
    item({ id: "old", title: "Old", addedAt: "2025-01-01T00:00:00Z", posterUrl: "/p/old", backdropUrl: "/b/old" }),
    item({ id: "new", title: "New", addedAt: "2026-05-01T00:00:00Z", posterUrl: "/p/new" }),
    item({ id: "show", kind: "series", title: "Show", posterUrl: "/p/show" }),
    item({ id: "ani", kind: "series", title: "Anime Show", anime: true }),
    item({ id: "animov", title: "Anime Film", anime: true }),
  ];

  it("splits titles into Movies, Shows and Anime like the browse filter", () => {
    const s = homeSections(items);
    expect(s.map((x) => [x.label, x.count])).toEqual([
      ["Movies", 2],
      ["Shows", 1],
      ["Anime", 2],
    ]);
  });

  it("lists the newest titles first and prefers a backdrop for the tile", () => {
    const movies = homeSections(items)[0];
    expect(movies.recent.map((it) => it.id)).toEqual(["new", "old"]);
    expect(movies.art).toBe("/b/old");
    expect(movies.artIsPoster).toBe(false);
    const shows = homeSections(items)[1];
    expect(shows.art).toBe("/p/show");
    expect(shows.artIsPoster).toBe(true);
  });

  it("leaves out empty sections and caps the rail", () => {
    const many = Array.from({ length: 30 }, (_, i) => item({ id: `m${i}` }));
    const s = homeSections(many, 10);
    expect(s).toHaveLength(1);
    expect(s[0].recent).toHaveLength(10);
  });
});

describe("cards", () => {
  it("prefers an episode still, then a backdrop, then a letterboxed poster", () => {
    expect(wideArt({ kind: "episode", id: "e", title: "S", thumb_url: "/t", backdrop_url: "/b", poster_url: "/p" })).toEqual({ url: "/t", poster: false });
    expect(wideArt({ kind: "movie", id: "m", title: "M", backdrop_url: "/b", poster_url: "/p" })).toEqual({ url: "/b", poster: false });
    expect(wideArt({ kind: "movie", id: "m", title: "M", poster_url: "/p" })).toEqual({ url: "/p", poster: true });
    expect(wideArt(undefined, "/fallback")).toEqual({ url: "/fallback", poster: true });
    expect(wideArt(undefined)).toEqual({ url: null, poster: false });
  });

  it("names continue items from their card, with a fallback for older servers", () => {
    const base = { item_kind: "episode", item_id: "e", position_ms: 1, duration_ms: 2 };
    expect(continueLabels({ ...base, title: "Pilot", card: { kind: "episode", id: "e", title: "Lost", subtitle: "S1:E1 - Pilot" } })).toEqual({
      title: "Lost",
      subtitle: "S1:E1 - Pilot",
    });
    expect(continueLabels({ ...base, title: "Pilot" })).toEqual({ title: "Pilot", subtitle: "" });
  });

  it("plays from the resume point", () => {
    expect(playHref("episode", "e1", 61000.7)).toBe("/watch/episode/e1?t=61000");
    expect(playHref("movie", "m1")).toBe("/watch/movie/m1");
    expect(progressOf({ item_kind: "movie", item_id: "m", position_ms: 50, resume_ms: 25, duration_ms: 100 })).toBe(0.25);
    expect(progressOf({ item_kind: "movie", item_id: "m", position_ms: 50, duration_ms: 0 })).toBe(0);
  });
});
