import { describe, expect, it } from "vitest";
import { nextPromptAtMs } from "./Player";

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
