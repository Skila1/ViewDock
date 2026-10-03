import { describe, expect, it } from "vitest";
import type { BrowseItem } from "./browse";
import { becauseLiked, DEFAULT_ROWS, genreAffinity, homeRows, inGenre, mergeContinue, recommendedItems } from "./homeRows";

const item = (id: string, genres: string[], year = 2000): BrowseItem => ({
  key: `movie:${id}`,
  kind: "movie",
  id,
  title: id,
  year,
  posterUrl: null,
  backdropUrl: null,
  unmatched: false,
  overview: "",
  genres,
  anime: false,
  addedAt: "",
  href: `/movies/${id}`,
});

const items = [
  item("cop", ["Action", "Comedy"], 1984),
  item("cop2", ["Action", "Comedy"], 1987),
  item("rush", ["Action", "Comedy"], 1998),
  item("guns", ["Action", "Comedy"], 2013),
  item("scream", ["Horror"], 1996),
  item("saw", ["Horror"], 2004),
];

describe("homeRows", () => {
  it("falls back to the default rows and drops unknown ids", () => {
    expect(homeRows([])).toEqual(DEFAULT_ROWS);
    expect(homeRows(["continue", "gone", "my_list"])).toEqual(["continue", "my_list"]);
    expect(DEFAULT_ROWS).not.toContain("next_up");
  });
});

describe("mergeContinue", () => {
  it("adds next episodes after in-progress titles, skipping shows already in progress", () => {
    const merged = mergeContinue(
      [{ item_kind: "episode", item_id: "e5", position_ms: 1, duration_ms: 2, card: { kind: "episode", id: "e5", title: "Lost", series_id: "lost" } }],
      [
        { kind: "episode", id: "e6", title: "Lost", series_id: "lost" },
        { kind: "episode", id: "f1", title: "Frieren", series_id: "frieren" },
      ],
    );
    expect(merged.map((e) => (e.kind === "progress" ? e.rec.item_id : e.card.id))).toEqual(["e5", "f1"]);
  });
});

describe("recommendation rows", () => {
  const signals = { views: {}, watched: ["movie:cop"], liked: ["movie:cop"], disliked: ["movie:scream"], not_interested: ["movie:guns"], recommended: ["movie:guns", "movie:rush", "movie:saw"] };

  it("weights genres by watching, liking and disliking", () => {
    const top = genreAffinity(items, signals);
    expect(top.map((g) => g.genre)).toEqual(["Action", "Comedy"]);
  });

  it("explains rows by the liked title and leaves out seen or rejected titles", () => {
    const rows = becauseLiked(items, signals);
    expect(rows).toHaveLength(1);
    expect(rows[0].source.id).toBe("cop");
    expect(rows[0].items.map((it) => it.id)).toEqual(expect.arrayContaining(["cop2", "rush"]));
    expect(rows[0].items.map((it) => it.id)).not.toContain("guns");
  });

  it("filters genre rows and server recommendations by the profile's rejections", () => {
    expect(inGenre(items, "horror", signals).map((it) => it.id)).toEqual(["saw"]);
    expect(recommendedItems(items, signals).map((it) => it.id)).toEqual(["rush", "saw"]);
  });
});
