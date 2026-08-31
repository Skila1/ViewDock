import { describe, expect, it } from "vitest";
import { sanitizeJourneyName, shouldShipAttach } from "./journey";

describe("sanitizeJourneyName", () => {
  it("accepts journey event names", () => {
    expect(sanitizeJourneyName("play.heartbeat")).toBe("play.heartbeat");
    expect(sanitizeJourneyName("land")).toBe("land");
  });

  it("rejects junk", () => {
    expect(sanitizeJourneyName("DROP TABLE")).toBe("");
    expect(sanitizeJourneyName("")).toBe("");
  });
});

describe("shouldShipAttach", () => {
  it("ships pause/seek/fullscreen and currentTime writes", () => {
    expect(shouldShipAttach("pause", true)).toBe(true);
    expect(shouldShipAttach("vd_seek", false)).toBe(true);
    expect(shouldShipAttach("vd_currentTime_write", false)).toBe(true);
    expect(shouldShipAttach("webkitbeginfullscreen", false)).toBe(true);
  });

  it("ships FRAG_CHANGED only while paused", () => {
    expect(shouldShipAttach("hls:FRAG_CHANGED", true)).toBe(true);
    expect(shouldShipAttach("hls:FRAG_CHANGED", false)).toBe(false);
  });

  it("does not ship noisy playlist polls or timeupdate", () => {
    expect(shouldShipAttach("seek_hold_poll", false)).toBe(false);
    expect(shouldShipAttach("timeupdate", true)).toBe(false);
    expect(shouldShipAttach("hls:LEVEL_LOADED", false)).toBe(false);
  });
});
