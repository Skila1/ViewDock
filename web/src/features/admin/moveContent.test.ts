import { describe, expect, it } from "vitest";
import type { Library, MovePlanItem } from "@/types/api.gen";
import { accepts, destinationOptions, formatBytes, groupIneligible, jobHeadline, managedFolder, planHeadline } from "./moveContent";

const lib = (id: string, content_type: Library["content_type"], name = id): Library => ({ id, name, path: `/media/${name}`, content_type, uploads_enabled: true });

describe("accepts", () => {
  it("matches the server's library type rules", () => {
    expect(accepts("movies", "movie")).toBe(true);
    expect(accepts("mixed", "movie")).toBe(true);
    expect(accepts("tv", "movie")).toBe(false);
    expect(accepts("tv", "series")).toBe(true);
    expect(accepts("mixed", "series")).toBe(true);
    expect(accepts("movies", "series")).toBe(false);
  });
});

describe("destinationOptions", () => {
  const libs = [lib("m", "movies", "Movies"), lib("t", "tv", "Shows"), lib("x", "mixed", "Mixed"), lib("m2", "movies", "Archive")];

  it("offers compatible libraries first and marks the rest incompatible", () => {
    const opts = destinationOptions(libs, ["movie"], ["m"]);
    expect(opts.map((o) => [o.library.id, o.compatible])).toEqual([
      ["m2", true],
      ["x", true],
      ["t", false],
    ]);
    expect(opts.find((o) => o.library.id === "t")?.note).toMatch(/cannot hold movies/);
  });

  it("shows only TV destinations for shows", () => {
    const ok = destinationOptions(libs, ["series"]).filter((o) => o.compatible).map((o) => o.library.id);
    expect(ok.sort()).toEqual(["t", "x"]);
  });

  it("explains partial moves from a Mixed library", () => {
    const opts = destinationOptions(libs, ["movie", "series"], ["x"]);
    expect(opts.every((o) => o.compatible)).toBe(true);
    expect(opts.find((o) => o.library.id === "m")?.note).toBe("Only movies can move here");
    expect(opts.find((o) => o.library.id === "t")?.note).toBe("Only TV shows can move here");
  });
});

describe("plan and job summaries", () => {
  const item = (over: Partial<MovePlanItem>): MovePlanItem => ({ kind: "series", id: "1", title: "Lost", files: 1, bytes: 1, ...over });

  it("counts eligible and skipped titles", () => {
    const plan = {
      destination: lib("m", "movies"),
      eligible: [item({ kind: "movie", id: "a", title: "Heat" }), item({ kind: "movie", id: "b", title: "Alien" })],
      ineligible: [item({ reason: "incompatible" })],
      eligible_bytes: 3 * 1024 ** 3,
    };
    expect(planHeadline(plan)).toBe("2 titles will move (3.0 GB); 1 will be skipped");
    expect(planHeadline({ ...plan, eligible: [], eligible_bytes: 0 })).toBe("Nothing can move; 1 will be skipped");
  });

  it("groups skipped titles by reason", () => {
    const groups = groupIneligible([
      item({ reason: "incompatible", title: "Lost" }),
      item({ reason: "incompatible", title: "Dark", year: 2017 }),
      item({ kind: "movie", reason: "duplicate", title: "Heat" }),
    ]);
    expect(groups[0]).toEqual({ reason: "incompatible", message: "TV shows cannot go into this library", titles: ["Lost", "Dark (2017)"] });
    expect(groups[1].message).toBe("Already in the destination library");
  });

  it("describes running and finished jobs", () => {
    const base = { id: "j", destination_library_id: "m", created_at: "", items: [], total: 5, skipped: 1 };
    expect(jobHeadline({ ...base, status: "running", moved: 2, failed: 0 })).toBe("Moving… 2 of 4");
    expect(jobHeadline({ ...base, status: "done", moved: 3, failed: 1 })).toBe("3 moved, 1 skipped, 1 failed and were left where they were");
  });

  it("formats sizes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(20 * 1024 ** 2)).toBe("20 MB");
  });
});

describe("managedFolder", () => {
  it("previews the folder ViewDock creates", () => {
    expect(managedFolder("/media", "Kids Movies")).toBe("/media/Kids Movies");
    expect(managedFolder("/media/", "../../etc")).toBe("/media/etc");
    expect(managedFolder(undefined, "")).toBe("/media/Library");
    expect(managedFolder("/media", "Tom & Jerry's: Best")).toBe("/media/Tom & Jerry's Best");
  });
});
