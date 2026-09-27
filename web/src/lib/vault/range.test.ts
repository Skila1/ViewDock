import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  chunkCount,
  chunkLength,
  chunkSpan,
  missingRuns,
  parseContentRange,
  parseRange,
  planChunks,
  retryDelay,
  runRange,
  type ByteRange,
} from "./range";

type SwHelpers = { parseRange: typeof parseRange; chunkSpan: typeof chunkSpan };

function loadServiceWorkerHelpers(): SwHelpers {
  const source = readFileSync(resolve(process.cwd(), "public", "sw.js"), "utf8");
  const match = /\/\/ vault-range:start([\s\S]*?)\/\/ vault-range:end/.exec(source);
  if (!match) throw new Error("sw.js is missing the vault-range markers");
  return new Function(`${match[1]}\nreturn { parseRange, chunkSpan };`)() as SwHelpers;
}

describe("chunk planning", () => {
  it("covers the file exactly with inclusive ranges", () => {
    expect(planChunks(10, 4)).toEqual([
      { index: 0, start: 0, end: 3 },
      { index: 1, start: 4, end: 7 },
      { index: 2, start: 8, end: 9 },
    ]);
    expect(planChunks(8, 4)).toHaveLength(2);
    expect(planChunks(0, 4)).toEqual([]);
    expect(chunkCount(1, 4)).toBe(1);
    expect(chunkLength(2, 10, 4)).toBe(2);
    expect(chunkLength(3, 10, 4)).toBe(0);
  });

  it("groups missing chunks into contiguous runs", () => {
    expect(missingRuns([null, null, "a", null, "b", null, null])).toEqual([
      { first: 0, last: 1 },
      { first: 3, last: 3 },
      { first: 5, last: 6 },
    ]);
    expect(missingRuns(["a", "b"])).toEqual([]);
    expect(runRange({ first: 1, last: 2 }, 10, 4)).toEqual({ start: 4, end: 9 });
  });

  it("parses Content-Range and rejects unknown totals", () => {
    expect(parseContentRange("bytes 0-3/10")).toEqual({ start: 0, end: 3, total: 10 });
    expect(parseContentRange("bytes 0-3/*")).toBeNull();
    expect(parseContentRange("bytes 5-3/10")).toBeNull();
    expect(parseContentRange("bytes 0-10/10")).toBeNull();
    expect(parseContentRange(null)).toBeNull();
  });

  it("backs off exponentially with jitter and a cap", () => {
    expect(retryDelay(0, () => 0)).toBe(500);
    expect(retryDelay(0, () => 1)).toBe(1000);
    expect(retryDelay(3, () => 1)).toBe(8000);
    expect(retryDelay(20, () => 1)).toBe(30_000);
  });
});

const RANGE_CASES: [string | null, number, ByteRange][] = [
  [null, 100, { kind: "full" }],
  ["bytes=0-", 100, { kind: "partial", start: 0, end: 99 }],
  ["bytes=10-19", 100, { kind: "partial", start: 10, end: 19 }],
  ["bytes=90-500", 100, { kind: "partial", start: 90, end: 99 }],
  ["bytes=-10", 100, { kind: "partial", start: 90, end: 99 }],
  ["bytes=-500", 100, { kind: "partial", start: 0, end: 99 }],
  ["bytes=100-", 100, { kind: "unsatisfiable" }],
  ["bytes=-0", 100, { kind: "unsatisfiable" }],
  ["bytes=20-10", 100, { kind: "full" }],
  ["bytes=0-1,5-6", 100, { kind: "full" }],
  ["items=0-1", 100, { kind: "full" }],
  ["bytes=-", 100, { kind: "full" }],
  [" BYTES = 5 - 6 ", 100, { kind: "partial", start: 5, end: 6 }],
  ["bytes=0-0", 0, { kind: "unsatisfiable" }],
];

describe("request Range parsing", () => {
  it.each(RANGE_CASES)("%s against %i bytes", (header, size, expected) => {
    expect(parseRange(header, size)).toEqual(expected);
  });

  it("maps a range onto the chunks that hold it", () => {
    expect(chunkSpan(0, 99, 40)).toEqual({ first: 0, last: 2 });
    expect(chunkSpan(40, 79, 40)).toEqual({ first: 1, last: 1 });
    expect(chunkSpan(39, 40, 40)).toEqual({ first: 0, last: 1 });
  });

  it("the service worker copy behaves identically", () => {
    const sw = loadServiceWorkerHelpers();
    for (const [header, size, expected] of RANGE_CASES) {
      expect(sw.parseRange(header, size)).toEqual(expected);
    }
    for (const [start, end, size] of [[0, 99, 40], [40, 79, 40], [39, 40, 40], [5, 5, 1]]) {
      expect(sw.chunkSpan(start, end, size)).toEqual(chunkSpan(start, end, size));
    }
  });
});
