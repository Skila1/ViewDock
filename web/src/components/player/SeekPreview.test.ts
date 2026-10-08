import { describe, expect, it } from "vitest";
import { previewFrameUrl } from "./SeekPreview";

describe("previewFrameUrl", () => {
  it("asks for the frame on the ten second grid", () => {
    expect(previewFrameUrl("/api/v1/playback/sessions/s1/preview?stoken=x", 27_900)).toBe("/api/v1/playback/sessions/s1/preview?stoken=x&t=20");
    expect(previewFrameUrl("/p", 9_999)).toBe("/p?t=0");
    expect(previewFrameUrl("/p", -5)).toBe("/p?t=0");
  });
});
