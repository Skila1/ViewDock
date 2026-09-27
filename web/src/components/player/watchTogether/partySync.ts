export type PartyMember = {
  id: string;
  display_name: string;
  host: boolean;
  ready: boolean;
  connected: boolean;
  buffering: boolean;
  eligible: boolean;
  drift_ms: number;
  kind?: string;
};

export type PartySyncInfo = {
  target_ms: number;
  hard_ms: number;
  eligible: number;
  max_drift_ms: number;
  stats: { reports: number; rate_corrections: number; seeks: number; realigns: number };
};

export type PartyState = {
  type: "state";
  room_id: string;
  host: string;
  playing: boolean;
  position_ms: number;
  server_ms: number;
  seq: number;
  members: PartyMember[];
  item_kind: string;
  item_id: string;
  shared_control: boolean;
  sync: PartySyncInfo;
  reason?: string;
  by?: string;
};

export type PartyCorrection = {
  action: "ok" | "rate" | "seek";
  rate: number;
  targetMs: number;
  driftMs: number;
  playing: boolean;
};

type Sample = { offset: number; rtt: number };

/**
 * Estimates the offset between this device's clock and the coordinator's from
 * ping round trips, trusting the sample with the smallest round trip.
 */
export class ClockSync {
  private samples: Sample[] = [];
  private readonly keep: number;

  constructor(keep = 8) {
    this.keep = keep;
  }

  add(t0: number, serverMs: number, t1: number): boolean {
    const rtt = t1 - t0;
    if (!Number.isFinite(rtt) || rtt < 0 || rtt > 10_000 || !Number.isFinite(serverMs)) return false;
    this.samples.push({ offset: serverMs - (t0 + t1) / 2, rtt });
    if (this.samples.length > this.keep) this.samples.shift();
    return true;
  }

  get ready(): boolean {
    return this.samples.length > 0;
  }

  private best(): Sample | undefined {
    let best: Sample | undefined;
    for (const s of this.samples) if (!best || s.rtt < best.rtt) best = s;
    return best;
  }

  get offsetMs(): number {
    return this.best()?.offset ?? 0;
  }

  get rttMs(): number {
    return this.best()?.rtt ?? 0;
  }

  serverNow(now = Date.now()): number {
    return now + this.offsetMs;
  }

  reset() {
    this.samples = [];
  }
}

/** Projects a timeline reported at serverMs to the server time `serverNow`. */
export function projectTimeline(positionMs: number, serverMs: number, playing: boolean, serverNow: number): number {
  if (!playing) return positionMs;
  return positionMs + Math.max(0, serverNow - serverMs);
}

export const MIN_RATE = 0.95;
export const MAX_RATE = 1.05;

export function clampRate(rate: number): number {
  if (!Number.isFinite(rate)) return 1;
  return Math.min(MAX_RATE, Math.max(MIN_RATE, rate));
}

/** Parses a coordinator sync message into a correction targeted at `serverNow`. */
export function parseCorrection(msg: Record<string, unknown>, serverNow: number): PartyCorrection | null {
  const action = msg.action;
  if (action !== "ok" && action !== "rate" && action !== "seek") return null;
  const playing = msg.playing !== false;
  const target = Number(msg.target_ms);
  const serverMs = Number(msg.server_ms);
  if (!Number.isFinite(target) || !Number.isFinite(serverMs)) return null;
  return {
    action,
    rate: action === "rate" ? clampRate(Number(msg.rate)) : 1,
    targetMs: projectTimeline(target, serverMs, playing, serverNow),
    driftMs: Number(msg.drift_ms) || 0,
    playing,
  };
}

/** Delay before reconnect attempt `attempt` (0 based): exponential, capped, jittered. */
export function reconnectDelay(attempt: number, random = Math.random): number {
  const base = Math.min(15_000, 1000 * 2 ** Math.min(attempt, 4));
  return Math.round(base * (0.75 + random() * 0.5));
}
