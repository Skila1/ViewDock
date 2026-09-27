import { describe, expect, it } from "vitest";
import { movieDurationSec, needsDurationPin, pinOpenMediaSource } from "./mediaDuration";

describe("mediaDuration", () => {
  it("converts ffprobe movie ms to seconds", () => {
    expect(movieDurationSec(10_193_184)).toBe(10193.184);
    expect(movieDurationSec(0)).toBeNull();
    expect(movieDurationSec(Number.POSITIVE_INFINITY)).toBeNull();
  });

  it("pins when MSE only knows the EVENT/appended window", () => {
    expect(needsDurationPin(1418.876, 10193.184)).toBe(true);
    expect(needsDurationPin(10193.184, 10193.184)).toBe(false);
    expect(needsDurationPin(Number.NaN, 10193.184)).toBe(true);
  });

  it("writes MediaSource.duration only while open and idle", () => {
    const ms = { readyState: "open", duration: 1418, sourceBuffers: { length: 0 } };
    expect(pinOpenMediaSource(ms, 10193.184)).toBe(true);
    expect(ms.duration).toBe(10193.184);
    expect(pinOpenMediaSource(ms, 10193.184)).toBe(false);
    expect(pinOpenMediaSource({ readyState: "closed", duration: 10 }, 100)).toBe(false);
  });
});
