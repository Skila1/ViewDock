import { describe, expect, it } from "vitest";
import type { Movie, Series } from "@/types/api.gen";
import { applyBrowse, browseParams, genresOf, parseBrowse, toBrowseItems, type BrowseQuery } from "./browse";

const movie = (id: string, title: string, extra: Partial<Movie> = {}): Movie => ({
  id,
  title,
  year: 2000,
  poster_url: null,
  unmatched: false,
  metadata_source: "tmdb",
  ...extra,
});
const show = (id: string, title: string, extra: Partial<Series> = {}): Series => ({
  id,
  title,
  year: 2010,
  poster_url: null,
  unmatched: false,
  metadata_source: "jellyfin",
  ...extra,
});

const items = toBrowseItems(
  [
    movie("m1", "Alien", { genres: ["Horror", "Science Fiction"], year: 1979, added_at: "2026-01-01" }),
    movie("m2", "Aladdin", { genres: ["Family"], year: 1992, added_at: "2026-03-01" }),
    movie("m3", "Your Name", { genres: ["Animation"], anime: true }),
  ],
  [show("s1", "Attack on Titan", { genres: ["Anime", "Action"], anime: true }), show("s2", "Severance", { genres: ["Drama"] })],
);
const base: BrowseQuery = { q: "", kind: "", genre: "", tag: "", sort: "" };
const ids = (q: Partial<BrowseQuery>, signals?: Parameters<typeof applyBrowse>[2]) =>
  applyBrowse(items, { ...base, ...q }, signals).map((i) => i.id);

describe("browse", () => {
  it("filters by media type, keeping anime separate", () => {
    expect(ids({ kind: "movie" })).toEqual(["m2", "m1"]);
    expect(ids({ kind: "series" })).toEqual(["s2"]);
    expect(ids({ kind: "anime" })).toEqual(["s1", "m3"]);
  });

  it("searches titles across movies and series, best match first", () => {
    expect(ids({ q: "al" })).toEqual(["m2", "m1"]);
    expect(ids({ q: "an" })).toEqual(["s1", "s2", "m3"]);
    expect(ids({ q: "titan" })).toEqual(["s1"]);
    expect(ids({ q: "horror" })).toEqual(["m1"]);
    expect(ids({ q: "zzz" })).toEqual([]);
  });

  it("filters by genre and sorts", () => {
    expect(ids({ genre: "family" })).toEqual(["m2"]);
    expect(ids({ kind: "movie", sort: "year" })).toEqual(["m2", "m1"]);
    expect(ids({ kind: "movie", sort: "added" })).toEqual(["m2", "m1"]);
    expect(ids({ kind: "movie", sort: "title_desc" })).toEqual(["m1", "m2"]);
  });

  it("applies the viewer's watch signals", () => {
    const signals = { views: { "movie:m1": 3, "series:s2": 5 }, watched: ["movie:m1"], recommended: ["series:s2", "movie:m2"] };
    expect(ids({ tag: "most_viewed" }, signals)).toEqual(["s2", "m1"]);
    expect(ids({ tag: "unwatched", kind: "movie" }, signals)).toEqual(["m2"]);
    expect(ids({ tag: "recommended" }, signals)).toEqual(["s2", "m2"]);
  });

  it("round-trips the query through the URL and ignores unknown values", () => {
    const q: BrowseQuery = { q: "alien", kind: "anime", genre: "Drama", tag: "unwatched", sort: "year" };
    expect(parseBrowse(browseParams(q))).toEqual(q);
    expect(parseBrowse(new URLSearchParams("type=bogus&tag=x&sort=y"))).toEqual(base);
  });

  it("lists genres by popularity", () => {
    expect(genresOf(items).slice(0, 2)).toEqual(["Action", "Animation"]);
  });
});
