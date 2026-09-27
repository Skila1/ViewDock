import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";

const replay = vi.fn(async () => ({ sent: 0, conflicts: 0, dropped: 0, failed: 0 }));
vi.mock("@/api/client", () => ({ replayOfflineMutations: replay }));

const { listQueuedMutations, removeQueuedMutation } = await import("@/lib/offlineQueue");
const { getRecord, putRecord, vaultKey } = await import("./db");
const { progressPath, recordVaultProgress } = await import("./progress");

describe("vault playback progress", () => {
  beforeEach(async () => {
    for (const m of await listQueuedMutations()) await removeQueuedMutation(m.id);
    replay.mockClear();
  });

  it("queues coalesced progress and records the local resume point", async () => {
    const key = vaultKey("alice", "movie", "m 1");
    await putRecord({
      key,
      userId: "alice",
      itemKind: "movie",
      itemId: "m 1",
      title: "M",
      mime: "video/mp4",
      size: 1,
      chunkSize: 1,
      status: "complete",
      hashes: ["x"],
      bytesStored: 1,
      createdAt: new Date().toISOString(),
    });
    const item = { key, itemKind: "movie" as const, itemId: "m 1" };
    await recordVaultProgress(item, 10_000.4, 60_000);
    await recordVaultProgress(item, 20_000, 60_000);

    const queued = await listQueuedMutations();
    expect(queued).toHaveLength(1);
    expect(queued[0]).toMatchObject({
      path: "/api/v1/progress/movie/m%201",
      method: "PUT",
      coalesceKey: "PUT /api/v1/progress/movie/m%201",
      body: { position_ms: 20_000, duration_ms: 60_000 },
    });
    expect(typeof (queued[0].body as { client_updated_at: string }).client_updated_at).toBe("string");
    expect((await getRecord(key))?.positionMs).toBe(20_000);
    expect(replay).toHaveBeenCalledTimes(2);
    expect(progressPath("episode", "a/b")).toBe("/api/v1/progress/episode/a%2Fb");
  });
});
