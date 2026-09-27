import { describe, expect, it } from "vitest";
import { ClockSync, clampRate, panelVisible, parseCorrection, projectTimeline, reconnectDelay } from "./partySync";

describe("panelVisible", () => {
  it("follows the host's setting by default", () => {
    expect(panelVisible("everyone", false, null)).toBe(true);
    expect(panelVisible("host", false, null)).toBe(false);
    expect(panelVisible("host", true, null)).toBe(true);
    expect(panelVisible("hidden", true, null)).toBe(false);
  });
  it("lets viewers close it and only the host reopen a hidden panel", () => {
    expect(panelVisible("everyone", false, "closed")).toBe(false);
    expect(panelVisible("hidden", true, "open")).toBe(true);
    expect(panelVisible("hidden", false, "open")).toBe(false);
    expect(panelVisible("host", false, "open")).toBe(false);
  });
});

describe("ClockSync", () => {
  it("uses the lowest round trip sample", () => {
    const c = new ClockSync();
    expect(c.ready).toBe(false);
    c.add(1000, 6100, 1200);
    c.add(2000, 7020, 2040);
    expect(c.rttMs).toBe(40);
    expect(c.offsetMs).toBe(5000);
    expect(c.serverNow(10_000)).toBe(15_000);
  });

  it("rejects impossible samples and keeps a bounded window", () => {
    const c = new ClockSync(2);
    expect(c.add(100, 0, 50)).toBe(false);
    expect(c.add(0, 0, 20_000)).toBe(false);
    c.add(0, 100, 10);
    c.add(0, 200, 30);
    c.add(0, 300, 20);
    expect(c.rttMs).toBe(20);
    expect(c.offsetMs).toBe(290);
  });
});

describe("projectTimeline", () => {
  it("advances only while playing", () => {
    expect(projectTimeline(10_000, 1000, true, 1500)).toBe(10_500);
    expect(projectTimeline(10_000, 1000, false, 1500)).toBe(10_000);
    expect(projectTimeline(10_000, 2000, true, 1500)).toBe(10_000);
  });
});

describe("parseCorrection", () => {
  it("projects seek targets to the current server time", () => {
    const c = parseCorrection({ action: "seek", target_ms: 60_000, server_ms: 5000, rate: 1, drift_ms: 1800, playing: true }, 5300);
    expect(c).toEqual({ action: "seek", rate: 1, targetMs: 60_300, driftMs: 1800, playing: true });
  });

  it("clamps rates and ignores unknown actions", () => {
    expect(parseCorrection({ action: "rate", rate: 2, target_ms: 0, server_ms: 0 }, 0)?.rate).toBe(1.05);
    expect(parseCorrection({ action: "resync", target_ms: 0, server_ms: 0 }, 0)).toBeNull();
    expect(clampRate(Number.NaN)).toBe(1);
  });
});

describe("reconnectDelay", () => {
  it("backs off exponentially up to 15 s with jitter", () => {
    expect(reconnectDelay(0, () => 0.5)).toBe(1000);
    expect(reconnectDelay(2, () => 0.5)).toBe(4000);
    expect(reconnectDelay(10, () => 0.5)).toBe(15_000);
    expect(reconnectDelay(10, () => 0)).toBe(11_250);
  });
});
