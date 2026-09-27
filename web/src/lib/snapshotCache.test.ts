import "fake-indexeddb/auto";
import { describe, expect, it } from "vitest";
import { clearSnapshots, isCacheable, loadSnapshot, saveSnapshot, setSnapshotScope } from "@/lib/snapshotCache";

describe("snapshot cache", () => {
  it("only allows catalogue and profile reads", () => {
    expect(isCacheable("/api/v1/movies?page=2")).toBe(true);
    expect(isCacheable("/api/v1/me")).toBe(true);
    expect(isCacheable("/api/v1/admin/users")).toBe(false);
    expect(isCacheable("/api/v1/auth/csrf")).toBe(false);
    expect(isCacheable("/api/v1/playback/sessions/x")).toBe(false);
  });

  it("scopes entries per user and clears on logout", async () => {
    setSnapshotScope("alice");
    await saveSnapshot("/api/v1/movies", { items: ["a"] });
    setSnapshotScope("bob");
    expect(await loadSnapshot("/api/v1/movies")).toBeNull();
    setSnapshotScope("alice");
    expect((await loadSnapshot<{ items: string[] }>("/api/v1/movies"))?.body.items).toEqual(["a"]);
    await clearSnapshots();
    expect(await loadSnapshot("/api/v1/movies")).toBeNull();
  });
});
