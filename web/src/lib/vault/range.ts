// Pure byte-range and chunk arithmetic shared by the downloader and the
// service worker. web/public/sw.js carries a copy of parseRange and chunkSpan
// between "vault-range" markers; range.test.ts runs both against one table.

export const DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024;

export type ChunkPlan = { index: number; start: number; end: number };
export type ByteRange = { kind: "full" } | { kind: "partial"; start: number; end: number } | { kind: "unsatisfiable" };
export type ContentRange = { start: number; end: number; total: number };
export type ChunkRun = { first: number; last: number };

export function chunkCount(size: number, chunkSize: number): number {
  if (!Number.isFinite(size) || size <= 0 || chunkSize <= 0) return 0;
  return Math.ceil(size / chunkSize);
}

/** planChunks splits [0, size) into inclusive byte ranges of chunkSize. */
export function planChunks(size: number, chunkSize = DEFAULT_CHUNK_SIZE): ChunkPlan[] {
  const count = chunkCount(size, chunkSize);
  const out: ChunkPlan[] = [];
  for (let index = 0; index < count; index++) {
    const start = index * chunkSize;
    out.push({ index, start, end: Math.min(size, start + chunkSize) - 1 });
  }
  return out;
}

export function chunkLength(index: number, size: number, chunkSize: number): number {
  return Math.max(0, Math.min(chunkSize, size - index * chunkSize));
}

/** missingRuns groups absent chunks into contiguous runs so each run is one request. */
export function missingRuns(hashes: readonly (string | null | undefined)[]): ChunkRun[] {
  const runs: ChunkRun[] = [];
  for (let i = 0; i < hashes.length; i++) {
    if (hashes[i]) continue;
    const last = runs[runs.length - 1];
    if (last && last.last === i - 1) last.last = i;
    else runs.push({ first: i, last: i });
  }
  return runs;
}

/** runRange converts a chunk run into an inclusive byte range for a Range header. */
export function runRange(run: ChunkRun, size: number, chunkSize: number): { start: number; end: number } {
  return { start: run.first * chunkSize, end: Math.min(size, (run.last + 1) * chunkSize) - 1 };
}

/** parseContentRange reads "bytes start-end/total"; unknown totals are rejected. */
export function parseContentRange(header: string | null | undefined): ContentRange | null {
  if (!header) return null;
  const match = /^\s*bytes\s+(\d+)-(\d+)\/(\d+)\s*$/i.exec(header);
  if (!match) return null;
  const start = Number(match[1]);
  const end = Number(match[2]);
  const total = Number(match[3]);
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || !Number.isSafeInteger(total)) return null;
  if (start > end || end >= total) return null;
  return { start, end, total };
}

// vault-range:start
/**
 * parseRange interprets a request Range header against a representation of
 * `size` bytes. Only a single byte range is honoured; malformed or multi-range
 * headers are ignored and the full body is served, as RFC 9110 permits.
 */
export function parseRange(header: string | null | undefined, size: number): ByteRange {
  if (!header) return { kind: "full" };
  const match = /^\s*bytes\s*=\s*(\d*)\s*-\s*(\d*)\s*$/i.exec(header);
  if (!match) return { kind: "full" };
  const [, a, b] = match;
  if (a === "" && b === "") return { kind: "full" };
  if (size <= 0) return { kind: "unsatisfiable" };
  if (a === "") {
    const suffix = Number(b);
    if (!Number.isSafeInteger(suffix)) return { kind: "full" };
    if (suffix === 0) return { kind: "unsatisfiable" };
    return { kind: "partial", start: Math.max(0, size - suffix), end: size - 1 };
  }
  const start = Number(a);
  if (!Number.isSafeInteger(start)) return { kind: "full" };
  if (start >= size) return { kind: "unsatisfiable" };
  if (b === "") return { kind: "partial", start, end: size - 1 };
  const end = Number(b);
  if (!Number.isSafeInteger(end) || end < start) return { kind: "full" };
  return { kind: "partial", start, end: Math.min(end, size - 1) };
}

/** chunkSpan returns the first and last chunk indexes covering [start, end]. */
export function chunkSpan(start: number, end: number, chunkSize: number): ChunkRun {
  return { first: Math.floor(start / chunkSize), last: Math.floor(end / chunkSize) };
}
// vault-range:end

/** retryDelay is capped exponential backoff with full jitter. */
export function retryDelay(attempt: number, random = Math.random, baseMs = 1000, maxMs = 30_000): number {
  const ceiling = Math.min(maxMs, baseMs * 2 ** Math.max(0, attempt));
  return Math.round(ceiling / 2 + random() * (ceiling / 2));
}
