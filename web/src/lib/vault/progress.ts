import { replayOfflineMutations } from "@/api/client";
import { enqueueMutation } from "@/lib/offlineQueue";
import { patchRecord, type VaultRecord } from "./db";

export function progressPath(itemKind: string, itemId: string): string {
  return `/api/v1/progress/${itemKind}/${encodeURIComponent(itemId)}`;
}

/**
 * recordVaultProgress stores the position locally for offline resume and
 * queues it for the server through the offline mutation queue. The coalesce
 * key matches the one api.putProgress uses, so only the newest position per
 * title waits for reconnection and it converges with online playback.
 */
export async function recordVaultProgress(record: Pick<VaultRecord, "key" | "itemKind" | "itemId">, positionMs: number, durationMs: number): Promise<void> {
  const position = Math.max(0, Math.round(positionMs));
  const duration = Math.max(0, Math.round(durationMs));
  const at = new Date().toISOString();
  await patchRecord(record.key, { positionMs: position, positionAt: at }).catch(() => undefined);
  const path = progressPath(record.itemKind, record.itemId);
  await enqueueMutation({
    path,
    method: "PUT",
    body: { position_ms: position, duration_ms: duration, client_updated_at: at },
    coalesceKey: `PUT ${path}`,
  });
  if (typeof navigator === "undefined" || navigator.onLine !== false) void replayOfflineMutations().catch(() => {});
}

/** Checkpoint cadence while playing; pause, seek, end and hide also checkpoint. */
export const PROGRESS_INTERVAL_MS = 30_000;
