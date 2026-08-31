import { ensureCsrf } from "@/api/client";
import { isAppleWebKitPlayer, isIOSDevice } from "@/lib/device";

export type JourneyDetails = Record<string, unknown>;

type JourneyEvent = {
  name: string;
  t: number;
  details?: JourneyDetails;
};

const NAME_RE = /^[a-z][a-z0-9._-]{0,63}$/;
const FLUSH_MS = 1500;
const BATCH = 8;

const rid = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
const queue: JourneyEvent[] = [];
const lastShip = new Map<string, number>();
let ctx: JourneyDetails = {};
let timer: number | null = null;
let flushing = false;

export function sanitizeJourneyName(name: string): string {
  const n = name.trim().toLowerCase();
  return NAME_RE.test(n) ? n : "";
}

export function setJourneyContext(patch: JourneyDetails) {
  ctx = { ...ctx, ...patch };
}

export function journeyMeta(): JourneyDetails {
  const nav = typeof navigator !== "undefined" ? navigator : undefined;
  const win = typeof window !== "undefined" ? window : undefined;
  return {
    rid,
    path: win?.location.pathname,
    search: win?.location.search,
    ua: nav?.userAgent,
    ios: isIOSDevice(),
    webkit: isAppleWebKitPlayer(),
    visible: typeof document !== "undefined" ? document.visibilityState : "unknown",
    vw: win?.innerWidth,
    vh: win?.innerHeight,
    ...ctx,
  };
}

export function shouldShipAttach(ev: string, paused: boolean): boolean {
  if (ev === "hls:FRAG_CHANGED") return paused;
  if (ev === "timeupdate" || ev === "progress" || ev === "seek_hold_poll" || ev.startsWith("hls:LEVEL")) {
    return false;
  }
  return (
    ev === "prepareVideo" ||
    ev === "engine_selected" ||
    ev === "session_created" ||
    ev === "session_replace_begin" ||
    ev === "session_replace_suppressed_after_fs" ||
    ev === "hls:MANIFEST_PARSED" ||
    ev === "hls:ERROR" ||
    ev === "hls:ERROR_fatal" ||
    ev.startsWith("seek_hold_") ||
    ev.startsWith("vd_") ||
    ev === "play" ||
    ev === "playing" ||
    ev === "pause" ||
    ev === "seeking" ||
    ev === "seeked" ||
    ev === "waiting" ||
    ev === "stalled" ||
    ev === "fullscreen_tap" ||
    ev === "fullscreen_exit_tap" ||
    ev === "webkitbeginfullscreen" ||
    ev === "webkitendfullscreen" ||
    ev === "webkitDisplayingFullscreen_changed" ||
    ev === "mms_duration_pin" ||
    ev === "webkit_pause_after_fs_resumed" ||
    ev === "slider_seek_ignored_after_fs"
  );
}

export function report(name: string, details?: JourneyDetails) {
  const ev = sanitizeJourneyName(name);
  if (!ev) return;
  queue.push({
    name: ev,
    t: Date.now(),
    details: { ...journeyMeta(), ...details },
  });
  if (queue.length >= BATCH) {
    void flush();
    return;
  }
  schedule();
}

export function reportThrottled(name: string, details: JourneyDetails | undefined, minMs: number, key = name) {
  const now = Date.now();
  const prev = lastShip.get(key) ?? 0;
  if (now - prev < minMs) return;
  lastShip.set(key, now);
  report(name, details);
}

function schedule() {
  if (timer != null || typeof window === "undefined") return;
  timer = window.setTimeout(() => {
    timer = null;
    void flush();
  }, FLUSH_MS);
}

export async function flush(keepalive = false) {
  if (flushing || queue.length === 0) return;
  if (timer != null && typeof window !== "undefined") {
    window.clearTimeout(timer);
    timer = null;
  }
  flushing = true;
  const events = queue.splice(0, BATCH);
  try {
    const token = await ensureCsrf();
    const headers: Record<string, string> = {
      Accept: "application/json",
      "Content-Type": "application/json",
    };
    if (token) headers["X-CSRF-Token"] = token;
    await fetch("/api/v1/client-logs", {
      method: "POST",
      credentials: "include",
      keepalive,
      headers,
      body: JSON.stringify({ events }),
    });
  } catch {
    queue.unshift(...events);
  } finally {
    flushing = false;
    if (queue.length) schedule();
  }
}

export function startJourney() {
  report("land", { href: typeof window !== "undefined" ? window.location.href : "" });
}

export function bindJourneyLifecycle() {
  if (typeof window === "undefined") return () => {};
  const onHide = () => {
    report("visibility", { state: document.visibilityState });
    void flush(true);
  };
  const onPageHide = () => {
    report("pagehide");
    void flush(true);
  };
  document.addEventListener("visibilitychange", onHide);
  window.addEventListener("pagehide", onPageHide);
  return () => {
    document.removeEventListener("visibilitychange", onHide);
    window.removeEventListener("pagehide", onPageHide);
  };
}
