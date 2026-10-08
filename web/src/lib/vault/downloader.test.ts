import { describe, expect, it, vi } from "vitest";
import { transfer, probe, VaultError, type TransferSink, type TransferState } from "./downloader";
import { chunkCount, parseRange } from "./range";

const CHUNK = 8;

function makeData(size: number, seed = 1): Uint8Array {
  const out = new Uint8Array(size);
  for (let i = 0; i < size; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

type ServerOpts = {
  etag?: string;
  ranges?: boolean;
  // Status codes returned, in order, before behaving normally.
  failures?: { status: number; body?: unknown }[];
  // Error the body stream after this many bytes, once.
  dropAfter?: number;
};

function fakeServer(initial: Uint8Array, opts: ServerOpts = {}) {
  let data = initial;
  let etag = opts.etag ?? '"v1"';
  const failures = [...(opts.failures ?? [])];
  let dropAfter = opts.dropAfter;
  const log: { url: string; range?: string; ifRange?: string }[] = [];

  const body = (bytes: Uint8Array) => {
    const drop = dropAfter;
    dropAfter = undefined;
    let sent = 0;
    return new ReadableStream<Uint8Array>({
      pull(controller) {
        if (sent >= bytes.length) {
          controller.close();
          return;
        }
        const size = Math.min(5, bytes.length - sent);
        if (drop !== undefined && sent + size > drop) {
          controller.error(new TypeError("network connection lost"));
          return;
        }
        controller.enqueue(bytes.slice(sent, sent + size));
        sent += size;
      },
    });
  };

  const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers = (init?.headers ?? {}) as Record<string, string>;
    const range = headers.Range;
    const ifRange = headers["If-Range"];
    log.push({ url: String(input), range, ifRange });
    const failure = failures.shift();
    if (failure) return new Response(failure.body ? JSON.stringify(failure.body) : null, { status: failure.status });
    const base = { "Content-Type": "video/mp4", ETag: etag, "Accept-Ranges": "bytes" };
    const parsed = opts.ranges === false || (ifRange && ifRange !== etag) ? ({ kind: "full" } as const) : parseRange(range, data.length);
    if (parsed.kind === "unsatisfiable") return new Response(null, { status: 416, headers: { "Content-Range": `bytes */${data.length}` } });
    if (parsed.kind === "full") return new Response(body(data), { status: 200, headers: { ...base, "Content-Length": String(data.length) } });
    const slice = data.slice(parsed.start, parsed.end + 1);
    return new Response(body(slice), {
      status: 206,
      headers: { ...base, "Content-Length": String(slice.length), "Content-Range": `bytes ${parsed.start}-${parsed.end}/${data.length}` },
    });
  });

  return {
    fetchImpl: fetchImpl as unknown as typeof fetch,
    log,
    replace(next: Uint8Array, nextEtag: string) {
      data = next;
      etag = nextEtag;
    },
  };
}

type MemState = TransferState & { chunks: Map<number, Uint8Array>; resets: number };

function emptyState(): MemState {
  return { size: 0, chunkSize: CHUNK, mime: "", hashes: [], bytesStored: 0, chunks: new Map(), resets: 0 };
}

const sink: TransferSink<MemState> = {
  commit: async (state: MemState, index: number, plain: Uint8Array<ArrayBuffer>): Promise<MemState> => {
    const chunks = new Map(state.chunks);
    chunks.set(index, plain.slice());
    const hashes = state.hashes.slice();
    hashes[index] = `h${index}`;
    return { ...state, chunks, hashes, bytesStored: state.bytesStored + plain.byteLength };
  },
  reset: async (state: MemState, meta: { size: number; etag?: string; mime: string }): Promise<MemState> => ({
    ...state,
    size: meta.size,
    etag: meta.etag,
    mime: meta.mime,
    hashes: new Array<string | null>(chunkCount(meta.size, state.chunkSize)).fill(null),
    bytesStored: 0,
    chunks: new Map<number, Uint8Array>(),
    resets: state.resets + 1,
  }),
};

function assemble(state: MemState): Uint8Array {
  const out = new Uint8Array(state.size);
  for (const [index, chunk] of state.chunks) out.set(chunk, index * state.chunkSize);
  return out;
}

const noSleep = async () => {};

describe("transfer", () => {
  it("downloads a new file in one open-ended range request", async () => {
    const data = makeData(30);
    const server = fakeServer(data);
    const state = await transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(state)).toEqual(data);
    expect(state).toMatchObject({ size: 30, etag: '"v1"', mime: "video/mp4", bytesStored: 30 });
    expect(server.log).toEqual([{ url: "/dl", range: "bytes=0-", ifRange: undefined }]);
  });

  it("pauses and resumes only the missing chunks with If-Range", async () => {
    const data = makeData(40);
    const server = fakeServer(data);
    const controller = new AbortController();
    let paused: MemState | undefined;
    await expect(
      transfer({
        state: emptyState(),
        sink,
        url: async () => "/dl",
        signal: controller.signal,
        fetchImpl: server.fetchImpl,
        onProgress: (s) => {
          paused = s;
          if (s.chunks.size === 2) controller.abort();
        },
      }),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(paused?.chunks.size).toBe(2);

    const resumed = await transfer({ state: paused!, sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(resumed)).toEqual(data);
    expect(resumed.resets).toBe(1);
    expect(server.log[1]).toEqual({ url: "/dl", range: "bytes=16-39", ifRange: '"v1"' });
  });

  it("fills holes with one request per missing run", async () => {
    const data = makeData(40);
    const server = fakeServer(data);
    let state = await sink.reset(emptyState(), { size: 40, etag: '"v1"', mime: "video/mp4" });
    for (const index of [1, 3]) state = await sink.commit(state, index, data.slice(index * CHUNK, (index + 1) * CHUNK));
    const done = await transfer({ state, sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(done)).toEqual(data);
    expect(server.log.map((l) => l.range)).toEqual(["bytes=0-7", "bytes=16-23", "bytes=32-39"]);
  });

  it("restarts from zero when the source changed between sessions", async () => {
    const server = fakeServer(makeData(40));
    const controller = new AbortController();
    let partial: MemState | undefined;
    await transfer({
      state: emptyState(),
      sink,
      url: async () => "/dl",
      signal: controller.signal,
      fetchImpl: server.fetchImpl,
      onProgress: (s) => {
        partial = s;
        if (s.chunks.size === 3) controller.abort();
      },
    }).catch(() => undefined);
    const replaced = makeData(36, 7);
    server.replace(replaced, '"v2"');
    const done = await transfer({ state: partial!, sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(done)).toEqual(replaced);
    expect(done).toMatchObject({ size: 36, etag: '"v2"', resets: 2 });
  });

  it("works with servers that ignore Range, skipping stored chunks on resume", async () => {
    const data = makeData(25);
    const server = fakeServer(data, { ranges: false });
    const controller = new AbortController();
    let partial: MemState | undefined;
    await transfer({
      state: emptyState(),
      sink,
      url: async () => "/dl",
      signal: controller.signal,
      fetchImpl: server.fetchImpl,
      onProgress: (s) => {
        partial = s;
        if (s.chunks.size === 1) controller.abort();
      },
    }).catch(() => undefined);
    const commits = vi.fn(sink.commit);
    const done = await transfer({ state: partial!, sink: { ...sink, commit: commits }, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(done)).toEqual(data);
    expect(commits).toHaveBeenCalledTimes(3);
    expect(done.resets).toBe(1);
  });

  it("retries transient failures with backoff", async () => {
    const data = makeData(20);
    const server = fakeServer(data, { failures: [{ status: 503 }, { status: 502 }] });
    const sleep = vi.fn(noSleep);
    const done = await transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep });
    expect(assemble(done)).toEqual(data);
    expect(sleep).toHaveBeenCalledTimes(2);
  });

  it("resumes after the connection drops mid-stream", async () => {
    const data = makeData(40);
    const server = fakeServer(data, { dropAfter: 21 });
    const done = await transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(done)).toEqual(data);
    expect(server.log.map((l) => l.range)).toEqual(["bytes=0-", "bytes=16-39"]);
  });

  it("opens a fresh session when the playback session expired", async () => {
    const data = makeData(10);
    const server = fakeServer(data, { failures: [{ status: 410, body: { code: "SESSION_GONE" } }] });
    const url = vi.fn(async (fresh: boolean) => (fresh ? "/fresh" : "/old"));
    const done = await transfer({ state: emptyState(), sink, url, signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep });
    expect(assemble(done)).toEqual(data);
    expect(url.mock.calls.map((c) => c[0])).toEqual([false, true]);
    expect(server.log.map((l) => l.url)).toEqual(["/old", "/fresh"]);
  });

  it("stops with a clear error when downloads are disabled or forbidden", async () => {
    const disabled = fakeServer(makeData(10), { failures: [{ status: 403, body: { code: "feature_disabled" } }] });
    await expect(transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: disabled.fetchImpl })).rejects.toMatchObject({ code: "disabled" });
    const forbidden = fakeServer(makeData(10), { failures: [{ status: 403, body: { code: "forbidden" } }] });
    await expect(transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: forbidden.fetchImpl })).rejects.toMatchObject({ code: "forbidden" });
  });

  it("refuses sizes the admission check rejects before storing anything", async () => {
    const server = fakeServer(makeData(40));
    const reset = vi.fn(sink.reset);
    await expect(
      transfer({
        state: emptyState(),
        sink: { ...sink, reset },
        url: async () => "/dl",
        signal: new AbortController().signal,
        fetchImpl: server.fetchImpl,
        admit: () => {
          throw new VaultError("quota", "full");
        },
      }),
    ).rejects.toMatchObject({ code: "quota" });
    expect(reset).not.toHaveBeenCalled();
  });

  it("gives up after repeated failures without progress", async () => {
    const server = fakeServer(makeData(10), { failures: Array.from({ length: 10 }, () => ({ status: 500 })) });
    await expect(transfer({ state: emptyState(), sink, url: async () => "/dl", signal: new AbortController().signal, fetchImpl: server.fetchImpl, sleep: noSleep, maxAttempts: 3 })).rejects.toMatchObject({ code: "network" });
    expect(server.log).toHaveLength(3);
  });
});

describe("probe", () => {
  it("reads size, validator and type from a one byte range", async () => {
    const server = fakeServer(makeData(123));
    expect(await probe("/dl", undefined, server.fetchImpl)).toEqual({ size: 123, etag: '"v1"', mime: "video/mp4", ranges: true });
    expect(server.log[0].range).toBe("bytes=0-0");
  });

  it("reports servers without Range support", async () => {
    const server = fakeServer(makeData(50), { ranges: false });
    expect(await probe("/dl", undefined, server.fetchImpl)).toMatchObject({ size: 50, ranges: false });
  });
});
