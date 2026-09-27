import type { TelemetryEvent, TelemetryResult } from "../../api/diagnostics";

export type TelemetrySender = (sessionId: string, events: TelemetryEvent[], correlationId?: string) => Promise<TelemetryResult>;

const MAX_BATCH = 50;
const MAX_QUEUE = 200;
const URGENT = new Set(["startup_failed", "failover", "error"]);

export function newCorrelationId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/**
 * Batches client playback telemetry per session. Delivery is best effort:
 * a rejected, rate limited or failed batch is dropped, never retried, so
 * telemetry can never add load to a struggling server.
 */
export class SessionTelemetry {
  private sessionId: string | null = null;
  private queue: TelemetryEvent[] = [];
  private readonly send: TelemetrySender;
  private readonly correlationId: string;
  private blockedUntil = 0;

  constructor(send: TelemetrySender, correlationId = newCorrelationId()) {
    this.send = send;
    this.correlationId = correlationId;
  }

  get session(): string | null {
    return this.sessionId;
  }

  /** Flushes events queued for the previous session, then targets the new one. */
  bind(sessionId: string | null): void {
    if (sessionId === this.sessionId) return;
    void this.flush();
    this.sessionId = sessionId;
    this.blockedUntil = 0;
  }

  record(type: string, data?: Record<string, unknown>, at = Date.now()): void {
    if (!this.sessionId) return;
    if (this.queue.length >= MAX_QUEUE) this.queue.shift();
    this.queue.push(data ? { type, at, data } : { type, at });
    if (URGENT.has(type)) void this.flush();
  }

  pending(): number {
    return this.queue.length;
  }

  async flush(now = Date.now()): Promise<void> {
    const id = this.sessionId;
    if (!id || this.queue.length === 0) return;
    if (now < this.blockedUntil) {
      this.queue = [];
      return;
    }
    const batches: TelemetryEvent[][] = [];
    while (this.queue.length) batches.push(this.queue.splice(0, MAX_BATCH));
    for (const batch of batches) {
      try {
        await this.send(id, batch, this.correlationId);
      } catch (e) {
        const status = (e as { status?: number }).status;
        if (status === 429) this.blockedUntil = Date.now() + 60_000;
        if (status === 410 && this.sessionId === id) this.sessionId = null;
        return;
      }
    }
  }
}
