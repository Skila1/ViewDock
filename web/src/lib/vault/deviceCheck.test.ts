import { describe, expect, it } from "vitest";
import { codecStatus, latencyAdvice, mbps, median, summarize, throughputAdvice, type DeviceCheck } from "./deviceCheck";

describe("device test scoring", () => {
  it("computes medians and throughput", () => {
    expect(median([30, 10, 20])).toBe(20);
    expect(median([10, 20, 30, 40])).toBe(25);
    expect(Number.isNaN(median([]))).toBe(true);
    expect(mbps(1_000_000, 1000)).toBe(8);
    expect(mbps(1, 0)).toBe(0);
  });

  it("turns measurements into guidance", () => {
    expect(throughputAdvice(2).status).toBe("fail");
    expect(throughputAdvice(5).status).toBe("warn");
    expect(throughputAdvice(12).guidance).toMatch(/1080p/);
    expect(latencyAdvice(50).status).toBe("pass");
    expect(latencyAdvice(200).status).toBe("warn");
    expect(latencyAdvice(600).status).toBe("fail");
  });

  it("prefers decoder results over canPlayType claims", () => {
    expect(codecStatus({ file: "probably", decoding: { supported: false } }).supported).toBe(false);
    expect(codecStatus({ file: "", mse: true }).supported).toBe(true);
    expect(codecStatus({ file: "maybe" }).supported).toBe(false);
    expect(codecStatus({ file: "probably" }).detail).toBe("Direct file: probably");
  });

  it("counts results by status", () => {
    const checks = ["pass", "pass", "fail", "info"].map((status, i) => ({ id: String(i), group: "video", label: "", status, evidence: "reported", detail: "" }) as DeviceCheck);
    expect(summarize(checks)).toEqual({ pass: 2, warn: 0, fail: 1, info: 1 });
  });
});
