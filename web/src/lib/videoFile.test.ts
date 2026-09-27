import { describe, expect, it } from "vitest";
import { isHiddenDropName, isVideoFilename, pickUploadFiles } from "./videoFile";

describe("videoFile", () => {
  it("rejects hidden and non-video names", () => {
    expect(isHiddenDropName(".movie.mkv")).toBe(true);
    expect(isHiddenDropName("Thumbs.db")).toBe(true);
    expect(isVideoFilename("Title (2024).mkv")).toBe(true);
    expect(isVideoFilename("notes.txt")).toBe(false);
    expect(isVideoFilename("../escape.mkv")).toBe(false);
  });

  it("filters a drop list", () => {
    const files = pickUploadFiles([
      new File([""], "Show.S01E01.mkv"),
      new File([""], ".DS_Store"),
      new File([""], "readme.md"),
    ]);
    expect(files.map((f) => f.name)).toEqual(["Show.S01E01.mkv"]);
  });
});
