import type { PlaybackSession } from "@/types/api.gen";
import type { EngineCaps, PlaybackEngine } from "./policy";
import { selectEngine as selectEnginePolicy } from "./policy";

/** The only legal reasons to POST a playback session. */
export type SessionCreateReason = "START" | "QUALITY" | "GONE";

export function isVodOnDemand(session: PlaybackSession | null | undefined): boolean {
  return Boolean(session?.vod_ondemand);
}

/** Movie-timeline origin. VOD is always 0; EVENT is the window start. */
export function sessionOriginMs(session: PlaybackSession | null | undefined, startAt = 0): number {
  if (isVodOnDemand(session)) return 0;
  return session?.seekable_from_ms ?? startAt;
}

export function logicalPositionMs(originMs: number, currentTimeSec: number): number {
  return originMs + currentTimeSec * 1000;
}

/** Far EVENT seek may recreate. Native VOD seek never does. */
export function seekReplacesSession(vod: boolean, inWindow: boolean): boolean {
  return !vod && !inWindow;
}

export function selectPlaybackEngine(
  session: PlaybackSession | null | undefined,
  caps: EngineCaps,
  ios = false,
): PlaybackEngine {
  return selectEnginePolicy(session?.delivery, caps, ios, session?.hls_attach);
}
