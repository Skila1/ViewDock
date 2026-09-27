import { describe, expect, it } from "vitest";
import {
  describeEventData,
  eventLabel,
  eventSeverity,
  formatAge,
  formatDuration,
  ms,
  parseCandidates,
  percent,
  scoreTone,
  statusLabel,
  statusTone,
} from "./format";

describe("diagnostics format helpers", () => {
  it("maps statuses to tones and labels", () => {
    expect(statusTone("ok")).toBe("text-ok");
    expect(statusTone("degraded")).toBe("text-warn");
    expect(statusTone("down")).toBe("text-danger");
    expect(statusTone("unconfigured")).toBe("text-dim");
    expect(statusLabel("down")).toBe("Offline");
    expect(statusLabel(undefined)).toBe("Unknown");
  });

  it("formats durations and ages", () => {
    expect(formatDuration(42)).toBe("42 s");
    expect(formatDuration(125)).toBe("2 min");
    expect(formatDuration(3 * 3600 + 5 * 60)).toBe("3 h 5 min");
    expect(formatDuration(50 * 3600)).toBe("2 d 2 h");
    expect(formatDuration(-1)).toBe("unknown");
    const now = Date.parse("2026-09-27T12:00:00Z");
    expect(formatAge("2026-09-27T11:59:58Z", now)).toBe("just now");
    expect(formatAge("2026-09-27T11:58:00Z", now)).toBe("2 min ago");
    expect(formatAge(undefined, now)).toBe("never");
    expect(formatAge("not a date", now)).toBe("unknown");
  });

  it("formats rates and latencies", () => {
    expect(percent(0.987)).toBe("99%");
    expect(ms(0)).toBe("n/a");
    expect(ms(840)).toBe("840 ms");
    expect(ms(12_500)).toBe("12.5 s");
  });

  it("dims scores without enough evidence", () => {
    expect(scoreTone(95, 1, 3)).toBe("text-dim");
    expect(scoreTone(95, 5, 3)).toBe("text-ok");
    expect(scoreTone(70, 5, 3)).toBe("text-warn");
    expect(scoreTone(40, 5, 3)).toBe("text-danger");
  });

  it("labels and grades flight events", () => {
    expect(eventLabel("source_failover")).toBe("Source failover");
    expect(eventLabel("progress_pause")).toBe("Progress pause");
    expect(eventLabel("custom_thing")).toBe("custom thing");
    const base = { at: "", session_id: "s" };
    expect(eventSeverity({ ...base, type: "error", data: { fatal: true } })).toBe("problem");
    expect(eventSeverity({ ...base, type: "error", data: { fatal: false } })).toBe("warning");
    expect(eventSeverity({ ...base, type: "source_failover", data: { ok: true } })).toBe("warning");
    expect(eventSeverity({ ...base, type: "pipeline_failed" })).toBe("problem");
    expect(eventSeverity({ ...base, type: "stall" })).toBe("warning");
    expect(eventSeverity({ ...base, type: "seek" })).toBe("info");
  });

  it("describes event data without server-filled keys or nested values", () => {
    expect(describeEventData({ ttff_ms: 1200, source: "file-1", device_class: "tv", nested: { a: 1 }, code: "X" })).toBe(
      "code X, ttff 1200 ms",
    );
    expect(describeEventData(undefined)).toBe("");
    expect(describeEventData({ a: 1, b: 2, c: 3 }, 2)).toBe("a 1, b 2");
  });

  it("parses candidate lists", () => {
    expect(parseCandidates(" a, b\nc  a ,, ")).toEqual(["a", "b", "c"]);
    expect(parseCandidates("")).toEqual([]);
  });
});
