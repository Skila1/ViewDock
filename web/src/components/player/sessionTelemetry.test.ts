import { describe, expect, it, vi } from "vitest";
import { SessionTelemetry, type TelemetrySender } from "./sessionTelemetry";

const ok = () => Promise.resolve({ accepted: 1, rejected: 0, dropped: 0 });

describe("SessionTelemetry", () => {
  it("ignores events before a session is bound", async () => {
    const send = vi.fn<TelemetrySender>(ok);
    const t = new SessionTelemetry(send, "corr");
    t.record("stall");
    await t.flush();
    expect(send).not.toHaveBeenCalled();
  });

  it("batches at most 50 events with the correlation id", async () => {
    const send = vi.fn<TelemetrySender>(ok);
    const t = new SessionTelemetry(send, "corr");
    t.bind("s1");
    for (let i = 0; i < 60; i++) t.record("buffer", { buffer_ms: i }, 1000 + i);
    await t.flush();
    expect(send).toHaveBeenCalledTimes(2);
    expect(send.mock.calls[0][0]).toBe("s1");
    expect(send.mock.calls[0][1]).toHaveLength(50);
    expect(send.mock.calls[1][1]).toHaveLength(10);
    expect(send.mock.calls[0][2]).toBe("corr");
    expect(t.pending()).toBe(0);
  });

  it("flushes urgent events immediately", () => {
    const send = vi.fn<TelemetrySender>(ok);
    const t = new SessionTelemetry(send, "corr");
    t.bind("s1");
    t.record("stall");
    expect(send).not.toHaveBeenCalled();
    t.record("failover", { reason: "node_unavailable" });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][1].map((e) => e.type)).toEqual(["stall", "failover"]);
  });

  it("sends queued events to the old session when rebinding", () => {
    const send = vi.fn<TelemetrySender>(ok);
    const t = new SessionTelemetry(send, "corr");
    t.bind("s1");
    t.record("stall");
    t.bind("s2");
    expect(send.mock.calls[0][0]).toBe("s1");
    expect(t.session).toBe("s2");
  });

  it("drops batches after a rate limit instead of retrying", async () => {
    const send = vi.fn<TelemetrySender>(() => Promise.reject(Object.assign(new Error("slow down"), { status: 429 })));
    const t = new SessionTelemetry(send, "corr");
    t.bind("s1");
    t.record("stall");
    await t.flush();
    expect(send).toHaveBeenCalledTimes(1);
    t.record("stall");
    await t.flush();
    expect(send).toHaveBeenCalledTimes(1);
    expect(t.pending()).toBe(0);
  });

  it("stops sending to a session that is gone", async () => {
    const send = vi.fn<TelemetrySender>(() => Promise.reject(Object.assign(new Error("gone"), { status: 410 })));
    const t = new SessionTelemetry(send, "corr");
    t.bind("s1");
    t.record("stall");
    await t.flush();
    expect(t.session).toBeNull();
    t.record("stall");
    expect(t.pending()).toBe(0);
  });
});
