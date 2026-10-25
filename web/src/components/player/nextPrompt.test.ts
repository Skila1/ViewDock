import { describe, expect, it } from "vitest";
import { nextPromptAtMs, skipTarget } from "./Player";

describe("nextPromptAtMs", () => {
  const dur = 24 * 60_000;
  it("appears when detected credits start", () => {
    expect(nextPromptAtMs(22 * 60_000, dur, 10)).toBe(22 * 60_000);
  });
  it("falls back to shortly before the end without credits", () => {
    expect(nextPromptAtMs(null, dur, 10)).toBe(dur - 25_000);
    expect(nextPromptAtMs(undefined, dur, 30)).toBe(dur - 35_000);
  });
  it("ignores credits that would cut the episode short or start at the very end", () => {
    expect(nextPromptAtMs(5 * 60_000, dur, 10)).toBe(dur - 25_000);
    expect(nextPromptAtMs(dur - 2000, dur, 10)).toBe(dur - 25_000);
  });
  it("never prompts on very short videos", () => {
    expect(nextPromptAtMs(null, 50_000, 10)).toBeNull();
  });
});

describe("skipTarget", () => {
  const recap = { start_ms: 0, end_ms: 40_000 };
  const intro = { start_ms: 40_000, end_ms: 90_000 };
  it("offers Skip Recap during the recap, then Skip Intro", () => {
    expect(skipTarget(5_000, recap, intro)).toEqual({ label: "Skip Recap", endMs: 40_000, source: "skip_recap" });
    expect(skipTarget(45_000, recap, intro)).toEqual({ label: "Skip Intro", endMs: 90_000, source: "skip_intro" });
  });
  it("offers nothing outside them, near their end or without markers", () => {
    expect(skipTarget(120_000, recap, intro)).toBeNull();
    expect(skipTarget(89_000, recap, intro)).toBeNull();
    expect(skipTarget(5_000, null, undefined)).toBeNull();
  });
});
