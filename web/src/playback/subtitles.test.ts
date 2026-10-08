import { describe, expect, it } from "vitest";
import { activeCues, parseCues, subtitleBurnsIn, subtitleLabel } from "./subtitles";

describe("parseCues", () => {
  it("parses WebVTT with cue ids, settings and markup", () => {
    const cues = parseCues(
      "WEBVTT\n\nNOTE made by hand\n\n1\n00:00:01.000 --> 00:00:03.500 align:center\n<v Daniel>Hello <i>there</i></v>\n\n00:01:02.250 --> 00:01:04.000\n<c.yellow>Line one</c>\nLine &amp; two\n",
    );
    expect(cues).toEqual([
      { startMs: 1000, endMs: 3500, text: "Hello <i>there</i>" },
      { startMs: 62_250, endMs: 64_000, text: "Line one\nLine & two" },
    ]);
  });

  it("parses SRT with decimal commas, CRLF and a BOM", () => {
    const cues = parseCues("\uFEFF1\r\n00:00:05,100 --> 00:00:07,000\r\nFirst\r\n\r\n2\r\n01:00:00,000 --> 01:00:02,500\r\n{\\an8}Second\r\n");
    expect(cues).toEqual([
      { startMs: 5100, endMs: 7000, text: "First" },
      { startMs: 3_600_000, endMs: 3_602_500, text: "Second" },
    ]);
  });

  it("drops malformed, empty and backwards cues and sorts the rest", () => {
    const cues = parseCues(
      "WEBVTT\n\n00:00:09.000 --> 00:00:10.000\nLate\n\nnot a cue\n\n00:00:05.000 --> 00:00:04.000\nBackwards\n\n00:00:06.000 --> 00:00:07.000\n\n00:00:02.000 --> 00:00:03.000\nEarly\n",
    );
    expect(cues.map((c) => c.text)).toEqual(["Early", "Late"]);
  });

  it("strips karaoke timestamp tags", () => {
    expect(parseCues("WEBVTT\n\n00:00.000 --> 00:02.000\nOne <00:00:01.000>two\n")[0].text).toBe("One two");
  });
});

describe("activeCues", () => {
  const cues = parseCues(
    "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nA\n\n00:00:02.000 --> 00:00:03.000\nB\n\n00:00:05.000 --> 00:00:06.000\nC\n",
  );

  it("returns overlapping cues in start order", () => {
    expect(activeCues(cues, 2500).map((c) => c.text)).toEqual(["A", "B"]);
    expect(activeCues(cues, 3500).map((c) => c.text)).toEqual(["A"]);
  });

  it("treats the end time as exclusive and handles gaps and edges", () => {
    expect(activeCues(cues, 4000)).toEqual([]);
    expect(activeCues(cues, 0)).toEqual([]);
    expect(activeCues(cues, 5000).map((c) => c.text)).toEqual(["C"]);
    expect(activeCues(cues, 90_000)).toEqual([]);
    expect(activeCues([], 1000)).toEqual([]);
  });
});

describe("subtitleLabel", () => {
  it("names tracks by title, then language", () => {
    expect(subtitleLabel({ language: "eng" }, 1)).toBe("English");
    expect(subtitleLabel({ language: "eng", sdh: true }, 1)).toBe("English (SDH)");
    expect(subtitleLabel({ title: "English SDH", sdh: true }, 1)).toBe("English SDH");
    expect(subtitleLabel({ language: "spa", forced: true }, 2)).toBe("Spanish (Forced)");
    expect(subtitleLabel({ language: "xho" }, 2)).toBe("XHO");
    expect(subtitleLabel({}, 3)).toBe("Track 3");
  });
});

describe("subtitleBurnsIn", () => {
  it("flags image and ASS tracks, not plain text", () => {
    expect(subtitleBurnsIn({ codec: "hdmv_pgs_subtitle" })).toBe(true);
    expect(subtitleBurnsIn({ codec: "dvd_subtitle" })).toBe(true);
    expect(subtitleBurnsIn({ codec: "ass" })).toBe(true);
    expect(subtitleBurnsIn({ codec: "subrip" })).toBe(false);
    expect(subtitleBurnsIn({ codec: "webvtt" })).toBe(false);
    expect(subtitleBurnsIn({})).toBe(false);
  });
});
