import type { ErrorReportRequest, PlaybackSession } from "@/types/api.gen";
import type { AttachTraceEvent, HlsErrorRec } from "./attachTrace";

export type ErrorStage = "startup" | "playback";

export type PlaybackErrorInput = {
  message: string;
  code: string;
  stage: ErrorStage;
  itemKind: string;
  itemId: string;
  title?: string;
  session?: PlaybackSession | null;
  engine?: string | null;
  positionMs?: number;
  video?: {
    readyState: number;
    networkState: number;
    currentTime: number;
    duration: number;
    paused: boolean;
    error?: { code: number; message: string } | null;
  } | null;
  trace?: { events: AttachTraceEvent[]; hlsErrors: HlsErrorRec[]; engineReason?: string; playlistType?: string };
  page?: string;
  userAgent?: string;
  mse?: boolean;
  nativeHls?: boolean;
  now?: number;
};

export type PlaybackErrorReport = {
  request: ErrorReportRequest;
  text: string;
};

const MAX_EVENTS = 40;
const MAX_HLS_ERRORS = 12;

/** Removes stream grants, stream tokens and query strings from diagnostic text. */
export function scrubDiagnostic(s: string): string {
  return s
    .replace(/\/media-sources\/stream\/[^/\s?#"]+/g, "/media-sources/stream/[redacted]")
    .replace(/stoken=[^&\s"]+/gi, "stoken=[redacted]")
    .replace(/((?:https?:\/\/[^\s"?]+|\/[^\s"?]*)\.(?:m3u8|ts|m4s|mp4))\?[^\s"]*/g, "$1?[query]")
    .replace(/\b(api_key|apikey|token|x-emby-token)=[^&\s"]+/gi, "$1=[redacted]");
}

function mediaErrorName(code: number): string {
  switch (code) {
    case 1:
      return "MEDIA_ERR_ABORTED";
    case 2:
      return "MEDIA_ERR_NETWORK";
    case 3:
      return "MEDIA_ERR_DECODE";
    case 4:
      return "MEDIA_ERR_SRC_NOT_SUPPORTED";
    default:
      return `code ${code}`;
  }
}

/** User-facing text for a media element error that stopped playback. */
export function mediaErrorMessage(code: number): string {
  switch (code) {
    case 2:
      return "Playback stopped: the connection to the media was lost.";
    case 3:
      return "Playback stopped: the browser could not decode the video.";
    case 4:
      return "Playback stopped: this browser cannot play this format.";
    default:
      return "Playback stopped because of a media error.";
  }
}

function num(n: number): string {
  return Number.isFinite(n) ? String(Math.round(n * 100) / 100) : String(n);
}

/**
 * Builds a playback error report: a structured request for the server and
 * the same facts as plain text for the copy button on the error screen.
 */
export function buildPlaybackErrorReport(input: PlaybackErrorInput): PlaybackErrorReport {
  const now = input.now ?? Date.now();
  const s = input.session;
  const v = input.video;
  const events = (input.trace?.events ?? []).slice(-MAX_EVENTS);
  const hlsErrors = (input.trace?.hlsErrors ?? []).slice(-MAX_HLS_ERRORS);

  const lines: string[] = [
    "ViewDock playback error",
    `Time: ${new Date(now).toISOString()}`,
    `Message: ${input.message}`,
    `Code: ${input.code}`,
    `Stage: ${input.stage}`,
  ];
  if (input.page) lines.push(`Page: ${input.page}`);
  if (input.title) lines.push(`Title: ${input.title}`);
  lines.push(`Item: ${input.itemKind} ${input.itemId}`);
  if (s) {
    lines.push(`Session: ${s.id}`);
    lines.push(`Mode: ${s.decision?.mode ?? "?"}, delivery: ${s.delivery ?? "?"}, playback: ${s.decision?.playback ?? "?"}`);
  }
  if (input.engine) lines.push(`Engine: ${input.engine}${input.trace?.engineReason ? ` (${input.trace.engineReason})` : ""}`);
  if (input.trace?.playlistType) lines.push(`Playlist type: ${input.trace.playlistType}`);
  if (input.positionMs != null) lines.push(`Position: ${Math.floor(input.positionMs / 1000)}s`);
  if (v) {
    const mediaErr = v.error ? `${mediaErrorName(v.error.code)}${v.error.message ? `: ${v.error.message}` : ""}` : "none";
    lines.push(
      `Video: readyState=${v.readyState} networkState=${v.networkState} currentTime=${num(v.currentTime)} duration=${num(v.duration)} paused=${v.paused} error=${mediaErr}`,
    );
  }
  if (input.mse != null || input.nativeHls != null) lines.push(`MSE: ${input.mse ? "yes" : "no"}, native HLS: ${input.nativeHls ? "yes" : "no"}`);
  if (input.userAgent) lines.push(`Browser: ${input.userAgent}`);

  if (hlsErrors.length) {
    lines.push("", "HLS errors:");
    for (const e of hlsErrors) {
      const parts = [e.type, e.details, e.fatal ? "fatal" : "", e.code != null ? `http ${e.code}` : "", e.reason, e.frag].filter(Boolean);
      lines.push(`  ${new Date(e.t).toISOString().slice(11, 23)} ${parts.join(" | ")}`);
    }
  }
  if (events.length) {
    lines.push("", "Player events:");
    for (const e of events) {
      lines.push(`  ${new Date(e.t).toISOString().slice(11, 23)} ${e.ev}${e.detail ? ` ${e.detail}` : ""}`);
    }
  }
  const text = scrubDiagnostic(lines.join("\n"));

  const context: Record<string, string | number | boolean> = {
    item_kind: input.itemKind,
    item_id: input.itemId,
  };
  if (input.title) context.title = input.title;
  if (input.page) context.page = scrubDiagnostic(input.page);
  if (s) {
    context.session_id = s.id;
    if (s.decision?.mode) context.mode = s.decision.mode;
    if (s.delivery) context.delivery = s.delivery;
  }
  if (input.engine) context.engine = input.engine;
  if (input.positionMs != null) context.position_s = Math.floor(input.positionMs / 1000);
  if (v) {
    context.ready_state = v.readyState;
    context.network_state = v.networkState;
    if (v.error) context.media_error = mediaErrorName(v.error.code);
  }
  if (input.mse != null) context.mse = input.mse;
  if (input.nativeHls != null) context.native_hls = input.nativeHls;

  return {
    request: {
      message: input.message.slice(0, 300),
      code: input.code,
      stage: input.stage,
      context,
      trace: text,
    },
    text,
  };
}
