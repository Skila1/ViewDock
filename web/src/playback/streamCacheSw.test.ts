import { Blob as NodeBlob } from "node:buffer";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";

const ORIGIN = "http://localhost";
const CHUNK = 1000;
const SIZE = 3500;
const NAME = "viewdock-stream-movie-m1";
const BASE = "/__stream-cache/v1";
const SRC = "/api/v1/media-sources/stream/tok/Videos/r1/stream?static=true";

type Listener = (event: unknown) => void;

function memoryCaches() {
  const store = new Map<string, Map<string, Response>>();
  const keyOf = (req: RequestInfo | URL) => new URL(typeof req === "string" ? req : req instanceof URL ? req.href : req.url, ORIGIN).href;
  const open = async (name: string) => {
    if (!store.has(name)) store.set(name, new Map());
    const entries = store.get(name)!;
    return {
      match: async (req: RequestInfo) => entries.get(keyOf(req))?.clone(),
      put: async (req: RequestInfo, res: Response) => void entries.set(keyOf(req), res),
      keys: async () => [...entries.keys()].map((k) => new Request(k)),
      delete: async (req: RequestInfo) => entries.delete(keyOf(req)),
    };
  };
  return {
    open,
    has: async (name: string) => store.has(name),
    keys: async () => [...store.keys()],
    delete: async (name: string) => store.delete(name),
    match: async () => undefined,
  };
}

function load(cachesImpl: ReturnType<typeof memoryCaches>) {
  // jsdom's Blob is not a body Node's Response understands.
  vi.stubGlobal("Blob", NodeBlob);
  const listeners: Record<string, Listener> = {};
  const fakeSelf = {
    location: { origin: ORIGIN },
    addEventListener: (type: string, fn: Listener) => {
      listeners[type] = fn;
    },
    skipWaiting: () => Promise.resolve(),
    clients: { claim: () => Promise.resolve() },
  };
  const source = readFileSync(resolve(process.cwd(), "public", "sw.js"), "utf8");
  new Function("self", "caches", source)(fakeSelf, cachesImpl);
  return async (range?: string) => {
    let response: Promise<Response> | undefined;
    const qs = new URLSearchParams({ c: NAME, k: BASE, u: SRC });
    const request = new Request(`${ORIGIN}/__stream-cache/serve?${qs}`, { headers: range ? { Range: range } : {} });
    listeners.fetch({ request, respondWith: (p: Promise<Response>) => (response = p) });
    if (!response) throw new Error("service worker did not respond");
    return response;
  };
}

const data = Uint8Array.from({ length: SIZE }, (_, i) => i & 0xff);

async function seed(cachesImpl: ReturnType<typeof memoryCaches>, chunks: number[]) {
  const cache = await cachesImpl.open(NAME);
  await cache.put(`${BASE}/meta`, new Response(JSON.stringify({ size: SIZE, type: "video/mp4", chunk: CHUNK })));
  for (const i of chunks) await cache.put(`${BASE}/${i}`, new Response(data.slice(i * CHUNK, (i + 1) * CHUNK)));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("service worker stream cache", () => {
  it("serves contiguous cached chunks as one partial response", async () => {
    const c = memoryCaches();
    await seed(c, [0, 1]);
    const network = vi.fn();
    vi.stubGlobal("fetch", network);
    const res = await load(c)("bytes=500-");
    expect(res.status).toBe(206);
    expect(res.headers.get("Content-Range")).toBe(`bytes 500-1999/${SIZE}`);
    expect(new Uint8Array(await res.arrayBuffer())).toEqual(data.slice(500, 2000));
    expect(network).not.toHaveBeenCalled();
  });

  it("reads a miss from the network only up to the next cached chunk", async () => {
    const c = memoryCaches();
    await seed(c, [2]);
    const network = vi.fn(async () => new Response("x", { status: 206 }));
    vi.stubGlobal("fetch", network);
    await load(c)("bytes=100-");
    expect(network).toHaveBeenCalledWith(`${ORIGIN}${SRC}`, expect.objectContaining({ headers: { Range: "bytes=100-1999" } }));
  });

  it("passes through when nothing is cached for the title", async () => {
    const c = memoryCaches();
    const network = vi.fn(async () => new Response("x", { status: 206 }));
    vi.stubGlobal("fetch", network);
    await load(c)("bytes=0-");
    expect(network).toHaveBeenCalledWith(`${ORIGIN}${SRC}`, expect.objectContaining({ headers: { Range: "bytes=0-" } }));
  });
});
