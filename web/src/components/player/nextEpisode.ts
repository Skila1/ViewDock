import { api } from "@/api/api";
import type { PlaybackSession } from "@/types/api.gen";
import { bufferCacheEnabled, prefetchOpening } from "./bufferCache";

/**
 * Next-episode prefetch. Near the end of an episode, with the rest of it
 * already cached and downloads well ahead of playback, ViewDock opens the
 * next episode's playback session and stores its opening in the buffer
 * cache. The next Player takes that session over, so the episode starts
 * from local storage.
 */

type Prepared = { session: PlaybackSession; at: number; keepAlive: number; abort: AbortController };

const prepared = new Map<string, Prepared>();
/** A prepared session older than this is ended rather than used. */
const MAX_AGE_MS = 10 * 60_000;
/** Opening stored ahead of the episode. */
const OPENING_SEC = 90;

function end(key: string, p: Prepared) {
  window.clearInterval(p.keepAlive);
  p.abort.abort();
  prepared.delete(key);
  void api.endSession(p.session.id).catch(() => undefined);
}

export function preparing(kind: string, id: string): boolean {
  return prepared.has(`${kind}:${id}`);
}

/** Opens and pre-buffers the next episode. Safe to call more than once. */
export async function prepareNext(id: string, quality: string | undefined): Promise<void> {
  const key = `episode:${id}`;
  if (prepared.has(key)) return;
  const session = await api.createSession({ item_kind: "episode", item_id: id, start_ms: 0, quality });
  if (!bufferCacheEnabled(session, { kind: "episode", id })) {
    void api.endSession(session.id).catch(() => undefined);
    return;
  }
  // Keep-alives hold the session without writing watch progress, so the
  // episode does not show as started.
  const keepAlive = window.setInterval(() => void api.keepAlive(session.id).catch(() => undefined), 15_000);
  const p: Prepared = { session, at: Date.now(), keepAlive, abort: new AbortController() };
  prepared.set(key, p);
  window.setTimeout(() => {
    if (prepared.get(key) === p) end(key, p);
  }, MAX_AGE_MS);
  await prefetchOpening(session, { kind: "episode", id, quality }, OPENING_SEC, p.abort.signal).catch(() => 0);
}

/** Hands a prepared session to the Player that plays it, or null. */
export function takePrepared(kind: string, id: string): PlaybackSession | null {
  const key = `${kind}:${id}`;
  const p = prepared.get(key);
  if (!p) return null;
  window.clearInterval(p.keepAlive);
  prepared.delete(key);
  if (Date.now() - p.at > MAX_AGE_MS) {
    void api.endSession(p.session.id).catch(() => undefined);
    return null;
  }
  return p.session;
}

/** Ends prepared sessions nobody took, for example when the viewer leaves. */
export function dropPrepared(except?: string) {
  for (const [key, p] of prepared) if (key !== except) end(key, p);
}
