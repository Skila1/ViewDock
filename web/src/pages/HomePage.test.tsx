import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HomeCard, Movie, ProgressRecord, Series } from "@/types/api.gen";

vi.mock("@/api/api", () => ({
  api: {
    listMovies: vi.fn(),
    listSeries: vi.fn(),
    browseSignals: vi.fn(),
    continueWatching: vi.fn(),
    nextUp: vi.fn(),
    getPreferences: vi.fn(async () => ({ audio_lang: "", subtitle_lang: "", subtitle_mode: "auto", autoplay: true, home_rows: [] })),
    watchlist: vi.fn(async () => []),
  },
}));

import { api } from "@/api/api";
import { HomePage } from "./HomePage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const movie = (id: string, title: string, over: Partial<Movie> = {}): Movie => ({
  id,
  title,
  year: 2020,
  poster_url: `/p/${id}`,
  unmatched: false,
  metadata_source: "tmdb",
  added_at: "2026-01-01T00:00:00Z",
  ...over,
});
const show = (id: string, title: string, over: Partial<Series> = {}): Series => ({
  id,
  title,
  year: 2019,
  poster_url: `/p/${id}`,
  unmatched: false,
  metadata_source: "tmdb",
  ...over,
});

let root: Root;

async function render(url: string) {
  const host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <HomePage />
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  );
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

const headings = () => Array.from(document.querySelectorAll("section[aria-label]")).map((s) => s.getAttribute("aria-label"));

beforeEach(() => {
  vi.mocked(api.listMovies).mockResolvedValue([
    movie("heat", "Heat", { backdrop_url: "/b/heat", added_at: "2026-02-01T00:00:00Z" }),
    movie("up", "Up"),
  ]);
  vi.mocked(api.listSeries).mockResolvedValue([show("lost", "Lost"), show("frieren", "Frieren", { anime: true })]);
  vi.mocked(api.browseSignals).mockResolvedValue({ views: {}, watched: [], recommended: [] });
  const cont: ProgressRecord[] = [
    {
      item_kind: "episode",
      item_id: "e5",
      position_ms: 600000,
      duration_ms: 2400000,
      resume_ms: 600000,
      card: { kind: "episode", id: "e5", title: "Lost", subtitle: "S1:E5 - White Rabbit", backdrop_url: "/b/lost" },
    },
  ];
  vi.mocked(api.continueWatching).mockResolvedValue(cont);
  const next: HomeCard[] = [{ kind: "episode", id: "e2", title: "Frieren", subtitle: "S2:E1", poster_url: "/p/frieren" }];
  vi.mocked(api.nextUp).mockResolvedValue(next);
});

afterEach(() => {
  act(() => root.unmount());
  vi.clearAllMocks();
});

describe("HomePage", () => {
  it("shows the library tiles and rails in Jellyfin's order", async () => {
    await render("/");
    expect(headings().filter((h) => h !== "Recently Released")).toEqual([
      "My Media",
      "Continue Watching",
      "Recently Added in Movies",
      "Recently Added in Shows",
      "Recently Added in Anime",
    ]);
    const tiles = Array.from(document.querySelectorAll('section[aria-label="My Media"] a')).map((a) => [a.textContent, a.getAttribute("href")]);
    expect(tiles).toEqual([
      ["Movies", "/?type=movie"],
      ["Shows", "/?type=series"],
      ["Anime", "/?type=anime"],
      ["All titles", "/?all=1"],
    ]);
    const resume = document.querySelector('section[aria-label="Continue Watching"] a')!;
    expect(resume.getAttribute("href")).toBe("/watch/episode/e5?t=600000");
    expect(resume.textContent).toContain("S1:E5 - White Rabbit");
    // The next episode of a show is promoted into Continue Watching.
    const cont = Array.from(document.querySelectorAll('section[aria-label="Continue Watching"] a')).map((a) => a.getAttribute("href"));
    expect(cont).toContain("/watch/episode/e2");
    const recent = Array.from(document.querySelectorAll('section[aria-label="Recently Added in Movies"] [role="listitem"]')).map((li) => li.textContent);
    expect(recent[0]).toContain("Heat"); // newest first
    expect(document.querySelector('a[href="/?type=movie&sort=added"]')).not.toBeNull();
    expect(document.body.textContent).not.toContain("Clear filters");
  });

  it("leaves out Continue Watching and Next Up when there is nothing to show", async () => {
    vi.mocked(api.continueWatching).mockResolvedValue([]);
    vi.mocked(api.nextUp).mockResolvedValue([]);
    await render("/");
    expect(headings()).not.toContain("Continue Watching");
    expect(headings()).not.toContain("Next Up");
  });

  it("shows the full grid for All titles and for filters", async () => {
    await render("/?all=1");
    expect(headings()).toEqual([]);
    expect(document.body.textContent).toContain("Everything");
    act(() => root.unmount());
    await render("/?type=movie");
    expect(document.body.textContent).toContain("Clear filters");
    expect(headings()).toEqual([]);
  });
});
