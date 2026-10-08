import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it } from "vitest";
import {
  enqueueMutation,
  listQueuedMutations,
  removeQueuedMutation,
  replayQueuedMutations,
  resolveConflict,
  type MutationResult,
} from "@/lib/offlineQueue";

async function clear() {
  for (const m of await listQueuedMutations()) await removeQueuedMutation(m.id);
}

describe("offline queue", () => {
  beforeEach(clear);

  it("coalesces pending mutations with the same key", async () => {
    for (let i = 0; i < 5; i++) {
      await enqueueMutation({ path: "/api/v1/progress/movie/a", method: "PUT", body: { position_ms: i }, coalesceKey: "PUT /api/v1/progress/movie/a" });
    }
    await enqueueMutation({ path: "/api/v1/me", method: "PATCH", body: { display_name: "x" } });
    const rows = await listQueuedMutations();
    expect(rows).toHaveLength(2);
    expect(rows.find((r) => r.path.includes("progress"))?.body).toEqual({ position_ms: 4 });
  });

  it("keeps order, stops on transient failure and records conflicts", async () => {
    await enqueueMutation({ path: "/a", method: "PUT", body: {} });
    await enqueueMutation({ path: "/b", method: "PUT", body: {} });
    await enqueueMutation({ path: "/c", method: "PUT", body: {} });
    const results: Record<string, MutationResult> = { "/a": "sent", "/b": "conflict", "/c": "failed" };
    const seen: string[] = [];
    const summary = await replayQueuedMutations(async (m) => {
      seen.push(m.path);
      return { result: results[m.path], detail: m.path === "/b" ? { server_position_ms: 5 } : undefined };
    });
    expect(seen).toEqual(["/a", "/b", "/c"]);
    expect(summary).toEqual({ sent: 1, conflicts: 1, dropped: 0, failed: 1 });
    const rows = await listQueuedMutations();
    expect(rows.map((r) => [r.path, r.status])).toEqual([
      ["/b", "conflict"],
      ["/c", "pending"],
    ]);
  });

  it("re-queues a kept conflict with force", async () => {
    const m = await enqueueMutation({ path: "/p", method: "PUT", body: { position_ms: 1 } });
    await replayQueuedMutations(async () => ({ result: "conflict" }));
    await resolveConflict(m.id, "keep");
    const [row] = await listQueuedMutations();
    expect(row.status).toBe("pending");
    expect(row.body).toEqual({ position_ms: 1, force: true });
    await resolveConflict(m.id, "discard");
    expect(await listQueuedMutations()).toHaveLength(0);
  });
});
