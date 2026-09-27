import { describe, expect, it } from "vitest";
import { ADMIN_LINKS, MEDIA_SUBNAV, settingsSubnav, underPath } from "./adminNav";
import { libraryType } from "./MediaSourcesPage";

describe("admin navigation", () => {
  it("groups media pages under one Media entry", () => {
    const paths = ADMIN_LINKS.map((l) => l.to);
    expect(paths).toContain("/admin/media");
    expect(paths).not.toContain("/admin/uploads");
    expect(paths).not.toContain("/admin/grants");
    expect(paths).not.toContain("/admin/media-sources");
    const media = MEDIA_SUBNAV.sections.flatMap((s) => s.links.map((l) => l.to));
    expect(media).toEqual(["/admin/media", "/admin/media/titles", "/admin/media/uploads", "/admin/media/access", "/admin/media/sources"]);
  });

  it("matches sub-routes without matching lookalike paths", () => {
    expect(underPath("/admin/media", "/admin/media")).toBe(true);
    expect(underPath("/admin/media/titles", "/admin/media")).toBe(true);
    expect(underPath("/admin/media-sources", "/admin/media")).toBe(false);
  });

  it("builds the settings sidebar from its categories", () => {
    const nav = settingsSubnav([{ id: "settings-playback", label: "Playback" }]);
    expect(nav.sections[0].links.map((l) => l.to)).toEqual(["/admin/settings#settings-playback"]);
    expect(nav.sections[1].links[0].to).toBe("/admin/settings#settings-history");
  });
});

describe("Jellyfin library types", () => {
  it("sorts libraries into Movies, TV shows, Anime and Mixed", () => {
    expect(libraryType({ id: "1", name: "Films", collection_type: "movies" })).toBe("Movies");
    expect(libraryType({ id: "2", name: "Shows", collection_type: "tvshows" })).toBe("TV shows");
    expect(libraryType({ id: "3", name: "Anime Series", collection_type: "tvshows" })).toBe("Anime");
    expect(libraryType({ id: "4", name: "Home videos", collection_type: "" })).toBe("Mixed");
  });
});
