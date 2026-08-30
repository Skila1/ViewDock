import { describe, expect, it } from "vitest";
import { classifyPauseSource } from "./pauseClassify";

describe("classifyPauseSource", () => {
  it("treats a recent user pause as user-initiated", () => {
    expect(classifyPauseSource({ userPauseAgeMs: 40, viewdockPauseAgeMs: 40 })).toBe("user");
  });

  it("treats a ViewDock pause() without a user action as viewdock", () => {
    expect(classifyPauseSource({ userPauseAgeMs: null, viewdockPauseAgeMs: 10 })).toBe("viewdock");
  });

  it("leaves AVKit/WebKit/hls.js pauses unclassified", () => {
    expect(classifyPauseSource({ userPauseAgeMs: 5_000, viewdockPauseAgeMs: null })).toBe(
      "unknown_avkit_or_webkit_or_hls",
    );
  });
});
