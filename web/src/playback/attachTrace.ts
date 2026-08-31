import { reportThrottled, shouldShipAttach } from "@/lib/journey";
import { classifyNetworkAbort } from "./abortClassify";
import { classifyPauseSource, type PauseVerdict } from "./pauseClassify";
import { inspectPlaylistBody, type PlaylistSnap } from "./playlistInspect";

export type AttachTraceEvent = {
  t: number;
  ev: string;
  detail?: string;
};

export type MediaSnap = {
  t: number;
  ev: string;
  session_id: string | null;
  currentSrc: string;
  currentTime: number;
  video_duration: number | string;
  seekable: string;
  paused: boolean;
  readyState: number;
  webkitDisplayingFullscreen?: boolean;
  webkitPresentationMode?: string;
  logical_ms?: number;
  origin_ms?: number;
};

export type CurrentTimeWrite = {
  t: number;
  reason: string;
  requested: number;
  previous: number;
  session_id: string | null;
  fullscreen: boolean;
  stack?: string;
};

export type LogicalPos = {
  t: number;
  ev: string;
  origin_ms: number;
  media_currentTime: number;
  logical_ms: number;
  webkitDisplayingFullscreen?: boolean;
};

export type HlsErrorRec = {
  t: number;
  type?: string;
  details?: string;
  fatal?: boolean;
  reason?: string;
  code?: number;
  frag?: string;
};

export type AbortSummary = {
  total: number;
  fragment: number;
  playlist: number;
  other: number;
  fatal: number;
  fullscreen_correlated: boolean;
  by_detail: Record<string, number>;
};

export type UserControl = {
  t: number;
  action: "play" | "pause";
  via: string;
};

export type ViewDockPauseCall = {
  t: number;
  reason: string;
  stack?: string;
  currentTime: number;
  fullscreen: boolean;
  visibility: string;
};

export type PauseAttribution = {
  t: number;
  currentTime: number;
  paused: boolean;
  fullscreen: boolean;
  visibility: string;
  last_user_control: UserControl | null;
  last_user_control_age_ms: number | null;
  viewdock_pause_call: ViewDockPauseCall | null;
  viewdock_pause_age_ms: number | null;
  verdict: PauseVerdict;
  preceding: AttachTraceEvent[];
};

type AttachTrace = {
  events: AttachTraceEvent[];
  fullscreenSnaps: MediaSnap[];
  windowEvents: AttachTraceEvent[];
  preBuffer: AttachTraceEvent[];
  currentTimeWrites: CurrentTimeWrite[];
  logicalPositions: LogicalPos[];
  playlistSnaps: PlaylistSnap[];
  hlsErrors: HlsErrorRec[];
  abortSummary: AbortSummary;
  userControls: UserControl[];
  viewdockPauses: ViewDockPauseCall[];
  pauseAttributions: PauseAttribution[];
  lastDisplayingFs?: boolean;
  fsOpenedAt?: number;
  fsClosedAt?: number;
  keepWindowUntil?: number;
  playlistUrl?: string;
  originMs?: number;
  sessionId?: string | null;
  engineReason?: string;
  hlsJsSupported?: boolean;
  mmsAvailable?: boolean;
  mseAvailable?: boolean;
  playlistType?: string;
  playlistDurationMs?: number;
  pinnedDurationSec?: number;
  airplayPolicy?: string;
};

const PRE_MS = 5_000;
const POST_MS = 5_000;

const store = new WeakMap<HTMLVideoElement, AttachTrace>();

function emptyAbort(): AbortSummary {
  return { total: 0, fragment: 0, playlist: 0, other: 0, fatal: 0, fullscreen_correlated: false, by_detail: {} };
}

function state(video: HTMLVideoElement): AttachTrace {
  let cur = store.get(video);
  if (!cur) {
    cur = {
      events: [],
      fullscreenSnaps: [],
      windowEvents: [],
      preBuffer: [],
      currentTimeWrites: [],
      logicalPositions: [],
      playlistSnaps: [],
      hlsErrors: [],
      abortSummary: emptyAbort(),
      userControls: [],
      viewdockPauses: [],
      pauseAttributions: [],
    };
    store.set(video, cur);
  }
  return cur;
}

function ranges(r: TimeRanges): string {
  const out: string[] = [];
  for (let i = 0; i < r.length; i++) out.push(`${r.start(i).toFixed(2)}-${r.end(i).toFixed(2)}`);
  return out.join(", ") || "none";
}

function inWindow(cur: AttachTrace, t = Date.now()): boolean {
  if (cur.fsOpenedAt && t >= cur.fsOpenedAt - PRE_MS && (cur.keepWindowUntil == null || t <= cur.keepWindowUntil)) {
    return true;
  }
  return false;
}

function pushWindow(cur: AttachTrace, ev: AttachTraceEvent) {
  const t = ev.t;
  if (inWindow(cur, t)) {
    cur.windowEvents.push(ev);
    if (cur.windowEvents.length > 200) cur.windowEvents.splice(0, cur.windowEvents.length - 200);
    return;
  }
  cur.preBuffer.push(ev);
  const cut = t - PRE_MS;
  while (cur.preBuffer.length && cur.preBuffer[0].t < cut) cur.preBuffer.shift();
}

export function captureMedia(video: HTMLVideoElement, ev: string, sessionId?: string | null): MediaSnap {
  const apple = video as HTMLVideoElement & {
    webkitDisplayingFullscreen?: boolean;
    webkitPresentationMode?: string;
  };
  const cur = state(video);
  const origin = cur.originMs ?? 0;
  return {
    t: Date.now(),
    ev,
    session_id: sessionId ?? cur.sessionId ?? null,
    currentSrc: video.currentSrc,
    currentTime: video.currentTime,
    video_duration: Number.isFinite(video.duration) ? video.duration : String(video.duration),
    seekable: ranges(video.seekable),
    paused: video.paused,
    readyState: video.readyState,
    webkitDisplayingFullscreen: apple.webkitDisplayingFullscreen,
    webkitPresentationMode: apple.webkitPresentationMode,
    origin_ms: origin,
    logical_ms: origin + (video.currentTime || 0) * 1000,
  };
}

export function noteMedia(video: HTMLVideoElement, ev: string, sessionId?: string | null) {
  const snap = captureMedia(video, ev, sessionId);
  const line: AttachTraceEvent = { t: snap.t, ev, detail: JSON.stringify(snap) };
  noteAttach(video, ev, line.detail);
  const cur = state(video);
  cur.fullscreenSnaps.push(snap);
  if (cur.fullscreenSnaps.length > 32) cur.fullscreenSnaps.splice(0, cur.fullscreenSnaps.length - 32);
  if (ev === "fullscreen_tap" || ev === "webkitbeginfullscreen") {
    openFsWindow(cur);
    cur.logicalPositions.push({
      t: snap.t,
      ev,
      origin_ms: snap.origin_ms ?? 0,
      media_currentTime: snap.currentTime,
      logical_ms: snap.logical_ms ?? 0,
      webkitDisplayingFullscreen: snap.webkitDisplayingFullscreen,
    });
    void capturePlaylist(video, ev === "webkitbeginfullscreen" ? "before_or_begin" : "tap");
  }
  if (ev === "webkitendfullscreen" || ev === "fullscreen_exit_tap") {
    cur.logicalPositions.push({
      t: snap.t,
      ev,
      origin_ms: snap.origin_ms ?? 0,
      media_currentTime: snap.currentTime,
      logical_ms: snap.logical_ms ?? 0,
      webkitDisplayingFullscreen: snap.webkitDisplayingFullscreen,
    });
    closeFsWindow(cur);
    void capturePlaylist(video, ev === "webkitendfullscreen" ? "after_exit" : "exit_tap");
  }
}

function openFsWindow(cur: AttachTrace) {
  const now = Date.now();
  if (!cur.fsOpenedAt) {
    cur.fsOpenedAt = now;
    cur.windowEvents.push(...cur.preBuffer);
    cur.preBuffer = [];
  }
  cur.keepWindowUntil = undefined;
  cur.fsClosedAt = undefined;
}

function closeFsWindow(cur: AttachTrace) {
  const now = Date.now();
  cur.fsClosedAt = now;
  cur.keepWindowUntil = now + POST_MS;
}

export function noteDisplayingFsChange(video: HTMLVideoElement, sessionId?: string | null) {
  const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
  const now = Boolean(apple.webkitDisplayingFullscreen);
  const cur = state(video);
  if (cur.lastDisplayingFs === now) return;
  cur.lastDisplayingFs = now;
  if (now) {
    openFsWindow(cur);
    window.setTimeout(() => void capturePlaylist(video, "during"), 2000);
  } else closeFsWindow(cur);
  noteMedia(video, "webkitDisplayingFullscreen_changed", sessionId);
}

export function noteAttach(video: HTMLVideoElement, ev: string, detail?: string) {
  const cur = state(video);
  const rec = { t: Date.now(), ev, detail };
  cur.events.push(rec);
  if (cur.events.length > 80) cur.events.splice(0, cur.events.length - 80);
  pushWindow(cur, rec);
  if (shouldShipAttach(ev, video.paused)) {
    const name = ev === "hls:FRAG_CHANGED" ? "play.frag_while_paused" : "play.attach";
    reportThrottled(name, {
      ev,
      detail,
      paused: video.paused,
      currentTime: video.currentTime,
      readyState: video.readyState,
      seeking: video.seeking,
      session_id: cur.sessionId ?? undefined,
    }, ev === "hls:FRAG_CHANGED" ? 400 : 200, ev);
  }
}

export function noteUserControl(video: HTMLVideoElement, action: "play" | "pause", via: string) {
  const cur = state(video);
  const rec: UserControl = { t: Date.now(), action, via };
  cur.userControls.push(rec);
  if (cur.userControls.length > 12) cur.userControls.splice(0, cur.userControls.length - 12);
  noteAttach(video, "vd_user_control", JSON.stringify(rec));
}

export function viewDockPause(video: HTMLVideoElement, reason: string) {
  const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
  const rec: ViewDockPauseCall = {
    t: Date.now(),
    reason,
    stack: new Error("video.pause").stack?.split("\n").slice(2, 8).join(" | "),
    currentTime: video.currentTime,
    fullscreen: Boolean(apple.webkitDisplayingFullscreen),
    visibility: typeof document !== "undefined" ? document.visibilityState : "unknown",
  };
  const cur = state(video);
  cur.viewdockPauses.push(rec);
  if (cur.viewdockPauses.length > 12) cur.viewdockPauses.splice(0, cur.viewdockPauses.length - 12);
  noteAttach(video, "vd_video_pause", JSON.stringify(rec));
  video.pause();
}

function notePauseEvent(video: HTMLVideoElement) {
  const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
  const cur = state(video);
  const now = Date.now();
  const lastUser = cur.userControls[cur.userControls.length - 1] ?? null;
  const lastVd = cur.viewdockPauses[cur.viewdockPauses.length - 1] ?? null;
  const userAge = lastUser && lastUser.action === "pause" ? now - lastUser.t : null;
  const vdAge = lastVd ? now - lastVd.t : null;
  const rec: PauseAttribution = {
    t: now,
    currentTime: video.currentTime,
    paused: video.paused,
    fullscreen: Boolean(apple.webkitDisplayingFullscreen),
    visibility: typeof document !== "undefined" ? document.visibilityState : "unknown",
    last_user_control: lastUser,
    last_user_control_age_ms: lastUser ? now - lastUser.t : null,
    viewdock_pause_call: lastVd,
    viewdock_pause_age_ms: vdAge,
    verdict: classifyPauseSource({ userPauseAgeMs: userAge, viewdockPauseAgeMs: vdAge }),
    preceding: cur.windowEvents.slice(-8).concat(cur.events.slice(-8)).slice(-8),
  };
  cur.pauseAttributions.push(rec);
  if (cur.pauseAttributions.length > 12) cur.pauseAttributions.splice(0, cur.pauseAttributions.length - 12);
  noteAttach(video, "vd_pause_attribution", JSON.stringify({
    verdict: rec.verdict,
    currentTime: rec.currentTime,
    fullscreen: rec.fullscreen,
    visibility: rec.visibility,
    last_user_control: rec.last_user_control,
    last_user_control_age_ms: rec.last_user_control_age_ms,
    viewdock_pause_reason: lastVd?.reason,
    viewdock_pause_age_ms: rec.viewdock_pause_age_ms,
  }));
}

export function noteMediaDom(video: HTMLVideoElement, ev: string) {
  const cur = state(video);
  if (ev === "pause") notePauseEvent(video);
  if (ev === "timeupdate" && video.paused) {
    reportThrottled("play.time_while_paused", {
      currentTime: video.currentTime,
      readyState: video.readyState,
      seeking: video.seeking,
      session_id: cur.sessionId ?? undefined,
    }, 500);
  }
  if (ev === "timeupdate") {
    if (!inWindow(cur) && !cur.fsOpenedAt) {
      const rec = { t: Date.now(), ev, detail: `t=${video.currentTime.toFixed(3)}` };
      cur.preBuffer.push(rec);
      const cut = rec.t - PRE_MS;
      while (cur.preBuffer.length && cur.preBuffer[0].t < cut) cur.preBuffer.shift();
      return;
    }
    const last = cur.windowEvents[cur.windowEvents.length - 1];
    if (last?.ev === "timeupdate" && recTooSoon(last.t, 900) && Math.abs(video.currentTime - parseT(last.detail)) < 0.4) {
      return;
    }
  }
  noteAttach(video, ev, `t=${video.currentTime.toFixed(3)} dur=${fmtDur(video.duration)} rs=${video.readyState} paused=${video.paused} seek=${ranges(video.seekable)}`);
}

function recTooSoon(prev: number, minMs: number) {
  return Date.now() - prev < minMs;
}

function parseT(detail?: string): number {
  const m = detail?.match(/t=([0-9.]+)/);
  return m ? Number(m[1]) : NaN;
}

function fmtDur(d: number): string {
  return Number.isFinite(d) ? d.toFixed(3) : String(d);
}

export function noteCurrentTimeWrite(
  video: HTMLVideoElement,
  requested: number,
  reason: string,
  sessionId?: string | null,
) {
  const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
  const rec: CurrentTimeWrite = {
    t: Date.now(),
    reason,
    requested,
    previous: video.currentTime,
    session_id: sessionId ?? state(video).sessionId ?? null,
    fullscreen: Boolean(apple.webkitDisplayingFullscreen),
    stack: new Error("currentTime write").stack?.split("\n").slice(2, 7).join(" | "),
  };
  const cur = state(video);
  cur.currentTimeWrites.push(rec);
  if (cur.currentTimeWrites.length > 40) cur.currentTimeWrites.splice(0, cur.currentTimeWrites.length - 40);
  noteAttach(video, "vd_currentTime_write", JSON.stringify(rec));
}

export function noteLogical(video: HTMLVideoElement, ev: string, originMs: number, sessionId?: string | null) {
  const apple = video as HTMLVideoElement & { webkitDisplayingFullscreen?: boolean };
  const rec: LogicalPos = {
    t: Date.now(),
    ev,
    origin_ms: originMs,
    media_currentTime: video.currentTime,
    logical_ms: originMs + (video.currentTime || 0) * 1000,
    webkitDisplayingFullscreen: apple.webkitDisplayingFullscreen,
  };
  const cur = state(video);
  cur.originMs = originMs;
  cur.sessionId = sessionId ?? cur.sessionId;
  cur.logicalPositions.push(rec);
  if (cur.logicalPositions.length > 24) cur.logicalPositions.splice(0, cur.logicalPositions.length - 24);
}

export function noteHlsError(video: HTMLVideoElement, data: {
  type?: string;
  details?: string;
  fatal?: boolean;
  reason?: string;
  error?: { message?: string };
  response?: { code?: number };
  frag?: { sn?: number; start?: number; duration?: number; url?: string; relurl?: string };
}) {
  const cur = state(video);
  const rec: HlsErrorRec = {
    t: Date.now(),
    type: data.type,
    details: data.details,
    fatal: data.fatal,
    reason: data.reason || data.error?.message,
    code: data.response?.code,
    frag: data.frag
      ? `sn=${data.frag.sn} start=${data.frag.start} dur=${data.frag.duration} url=${(data.frag.relurl || data.frag.url || "").split("/").pop()}`
      : undefined,
  };
  const aborted = /abort/i.test(`${data.details ?? ""} ${data.reason ?? ""} ${data.error?.message ?? ""}`);
  if (aborted) {
    cur.abortSummary.total += 1;
    if (data.fatal) cur.abortSummary.fatal += 1;
    if (cur.fsOpenedAt && (cur.keepWindowUntil == null || Date.now() <= cur.keepWindowUntil)) {
      cur.abortSummary.fullscreen_correlated = true;
    }
    const kind = classifyNetworkAbort(data);
    if (kind === "fragment") cur.abortSummary.fragment += 1;
    else if (kind === "playlist") cur.abortSummary.playlist += 1;
    else cur.abortSummary.other += 1;
    const key = data.details || data.type || "unknown";
    cur.abortSummary.by_detail[key] = (cur.abortSummary.by_detail[key] ?? 0) + 1;
    if ((cur.abortSummary.by_detail[key] ?? 0) > 2) return;
  }
  cur.hlsErrors.push(rec);
  if (cur.hlsErrors.length > 30) cur.hlsErrors.splice(0, cur.hlsErrors.length - 30);
  noteAttach(video, data.fatal ? "hls:ERROR_fatal" : "hls:ERROR", JSON.stringify(rec));
}

export async function capturePlaylist(video: HTMLVideoElement, when: string) {
  const cur = state(video);
  const url = cur.playlistUrl;
  if (!url) return;
  try {
    const res = await fetch(url, { credentials: "include" });
    const text = await res.text();
    const snap = inspectPlaylistBody(text, when, res.headers);
    cur.playlistSnaps.push(snap);
    if (cur.playlistSnaps.length > 8) cur.playlistSnaps.splice(0, cur.playlistSnaps.length - 8);
    noteAttach(video, "playlist_snap", JSON.stringify(snap));
  } catch (err) {
    noteAttach(video, "playlist_snap_error", err instanceof Error ? err.message : "fetch failed");
  }
}

export function isFsWindow(video: HTMLVideoElement): boolean {
  return inWindow(state(video));
}

export function setAttachMeta(video: HTMLVideoElement, patch: Partial<AttachTrace>) {
  Object.assign(state(video), patch);
}

export function readAttachTrace(video: HTMLVideoElement | null): AttachTrace {
  const empty: AttachTrace = {
    events: [],
    fullscreenSnaps: [],
    windowEvents: [],
    preBuffer: [],
    currentTimeWrites: [],
    logicalPositions: [],
    playlistSnaps: [],
    hlsErrors: [],
    abortSummary: emptyAbort(),
    userControls: [],
    viewdockPauses: [],
    pauseAttributions: [],
  };
  if (!video) return empty;
  const cur = store.get(video);
  if (!cur) return empty;
  return {
    ...cur,
    events: [...cur.events],
    fullscreenSnaps: [...cur.fullscreenSnaps],
    windowEvents: [...cur.windowEvents],
    preBuffer: [...cur.preBuffer],
    currentTimeWrites: [...cur.currentTimeWrites],
    logicalPositions: [...cur.logicalPositions],
    playlistSnaps: [...cur.playlistSnaps],
    hlsErrors: [...cur.hlsErrors],
    abortSummary: { ...cur.abortSummary, by_detail: { ...cur.abortSummary.by_detail } },
    userControls: [...cur.userControls],
    viewdockPauses: [...cur.viewdockPauses],
    pauseAttributions: [...cur.pauseAttributions],
  };
}
