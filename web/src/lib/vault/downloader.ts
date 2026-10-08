import { chunkCount, chunkLength, missingRuns, parseContentRange, retryDelay, runRange } from "./range";

export type VaultErrorCode = "quota" | "too_large" | "forbidden" | "disabled" | "auth" | "gone" | "network" | "protocol" | "busy";

export class VaultError extends Error {
  code: VaultErrorCode;
  constructor(code: VaultErrorCode, message: string) {
    super(message);
    this.name = "VaultError";
    this.code = code;
  }
}

/** The subset of a vault record the transfer loop reads and advances. */
export type TransferState = {
  size: number;
  chunkSize: number;
  etag?: string;
  mime: string;
  hashes: (string | null)[];
  bytesStored: number;
};

export type TransferSink<T extends TransferState> = {
  /** Store one complete plaintext chunk and return the advanced state. */
  commit: (state: T, index: number, plain: Uint8Array<ArrayBuffer>) => Promise<T>;
  /** The source changed or first became known: discard stored chunks and adopt meta. */
  reset: (state: T, meta: { size: number; etag?: string; mime: string }) => Promise<T>;
};

export type TransferOptions<T extends TransferState> = {
  state: T;
  sink: TransferSink<T>;
  /** Returns the download URL; fresh asks for a new playback session. */
  url: (fresh: boolean) => Promise<string>;
  signal: AbortSignal;
  fetchImpl?: typeof fetch;
  onProgress?: (state: T) => void;
  /** Consecutive failures without progress before giving up. */
  maxAttempts?: number;
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  /** Rejects a size before any chunk is stored (quota and policy checks). */
  admit?: (size: number) => Promise<void> | void;
};

function abortError(): DOMException {
  return new DOMException("download paused", "AbortError");
}

export function sleepFor(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError());
      return;
    }
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

class Retry extends Error {
  fresh: boolean;
  constructor(message: string, fresh = false) {
    super(message);
    this.fresh = fresh;
  }
}

async function errorCode(res: Response): Promise<string | undefined> {
  const body = (await res.clone().json().catch(() => null)) as { code?: string } | null;
  return body?.code;
}

/**
 * transfer downloads every missing chunk of state with HTTP Range requests,
 * one request per contiguous run. It resumes from the stored digests, sends
 * If-Range so a replaced source restarts cleanly, tolerates servers that
 * ignore Range, and retries transient failures with jittered backoff. Aborting
 * the signal pauses; stored chunks are kept.
 */
export async function transfer<T extends TransferState>(opts: TransferOptions<T>): Promise<T> {
  const fetchImpl = opts.fetchImpl ?? fetch.bind(globalThis);
  const sleep = opts.sleep ?? sleepFor;
  const maxAttempts = opts.maxAttempts ?? 8;
  let state = opts.state;
  let attempts = 0;
  let fresh = false;

  const adopt = async (meta: { size: number; etag?: string; mime: string }) => {
    if (!Number.isSafeInteger(meta.size) || meta.size <= 0) throw new VaultError("protocol", "The server did not report the file size.");
    await opts.admit?.(meta.size);
    state = await opts.sink.reset(state, meta);
  };

  for (;;) {
    if (opts.signal.aborted) throw abortError();
    const known = state.size > 0 && state.hashes.length === chunkCount(state.size, state.chunkSize);
    const runs = known ? missingRuns(state.hashes) : [{ first: 0, last: Number.MAX_SAFE_INTEGER }];
    if (known && runs.length === 0) return state;
    const run = runs[0];
    const range = known ? runRange(run, state.size, state.chunkSize) : { start: 0, end: undefined };
    const headers: Record<string, string> = { Range: `bytes=${range.start}-${range.end ?? ""}` };
    if (known && state.etag) headers["If-Range"] = state.etag;

    try {
      const url = await opts.url(fresh);
      fresh = false;
      let res: Response;
      try {
        res = await fetchImpl(url, { headers, signal: opts.signal, credentials: "include", cache: "no-store" });
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") throw error;
        throw new Retry("network unreachable");
      }

      let firstIndex: number;
      let lastIndex: number;
      if (res.status === 206) {
        const cr = parseContentRange(res.headers.get("Content-Range"));
        if (!cr) throw new VaultError("protocol", "The server sent a partial response without a valid Content-Range.");
        const etag = res.headers.get("ETag") ?? undefined;
        const mime = res.headers.get("Content-Type") || state.mime;
        if (!known || cr.total !== state.size || (etag && state.etag && etag !== state.etag)) {
          await adopt({ size: cr.total, etag, mime });
          if (cr.start !== 0) {
            void res.body?.cancel().catch(() => {});
            continue;
          }
          firstIndex = 0;
        } else {
          if (cr.start !== range.start) throw new VaultError("protocol", "The server returned a different byte range than requested.");
          firstIndex = run.first;
        }
        lastIndex = Math.floor(cr.end / state.chunkSize);
      } else if (res.status === 200) {
        // Range ignored, or If-Range failed because the file changed.
        const total = Number(res.headers.get("Content-Length"));
        const etag = res.headers.get("ETag") ?? undefined;
        const mime = res.headers.get("Content-Type") || state.mime;
        if (!known || total !== state.size || etag !== state.etag) {
          await adopt({ size: total, etag, mime });
        }
        firstIndex = 0;
        lastIndex = chunkCount(state.size, state.chunkSize) - 1;
      } else if (res.status === 416) {
        void res.body?.cancel().catch(() => {});
        state = await opts.sink.reset(state, { size: 0, etag: undefined, mime: state.mime });
        throw new Retry("range no longer satisfiable");
      } else if (res.status === 401) {
        throw new VaultError("auth", "Your sign-in expired. Sign in again to continue downloading.");
      } else if (res.status === 403) {
        const code = await errorCode(res);
        if (code === "feature_disabled") throw new VaultError("disabled", "Downloads are turned off by the administrator.");
        throw new VaultError("forbidden", "You are not allowed to download this title.");
      } else if (res.status === 404 || res.status === 410) {
        if ((await errorCode(res)) === "not_found" && attempts > 0) throw new VaultError("gone", "This title is no longer available for download.");
        throw new Retry("playback session expired", true);
      } else if (res.status === 408 || res.status === 429 || res.status >= 500) {
        throw new Retry(`server responded ${res.status}`);
      } else {
        throw new VaultError("protocol", `Download failed (${res.status}).`);
      }

      if (!res.body) throw new VaultError("protocol", "The server response had no body.");
      const before = state.bytesStored;
      const holder = { state };
      try {
        await consume(res.body, holder, firstIndex, lastIndex, opts);
      } finally {
        // Chunks committed before a failure are kept, so a retry skips them.
        state = holder.state;
        if (state.bytesStored > before) attempts = 0;
      }
    } catch (error) {
      if (!(error instanceof Retry)) throw error;
      attempts++;
      if (attempts >= maxAttempts) throw new VaultError("network", `Download stopped after repeated failures (${error.message}).`);
      fresh = fresh || error.fresh;
      if (!error.fresh) await sleep(retryDelay(attempts - 1), opts.signal);
    }
  }
}

export type ProbeResult = { size: number; etag?: string; mime: string; ranges: boolean };

/** probe asks for one byte to learn the size, validator and type before consent. */
export async function probe(url: string, signal?: AbortSignal, fetchImpl: typeof fetch = fetch.bind(globalThis)): Promise<ProbeResult> {
  let res: Response;
  try {
    res = await fetchImpl(url, { headers: { Range: "bytes=0-0" }, signal, credentials: "include", cache: "no-store" });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    throw new VaultError("network", "The server could not be reached.");
  }
  const etag = res.headers.get("ETag") ?? undefined;
  const mime = res.headers.get("Content-Type") || "video/mp4";
  if (res.status === 206) {
    void res.body?.cancel().catch(() => {});
    const cr = parseContentRange(res.headers.get("Content-Range"));
    if (!cr) throw new VaultError("protocol", "The server did not report the file size.");
    return { size: cr.total, etag, mime, ranges: true };
  }
  if (res.status === 200) {
    void res.body?.cancel().catch(() => {});
    const size = Number(res.headers.get("Content-Length"));
    if (!Number.isSafeInteger(size) || size <= 0) throw new VaultError("protocol", "The server did not report the file size.");
    return { size, etag, mime, ranges: false };
  }
  if (res.status === 401) throw new VaultError("auth", "Your sign-in expired. Sign in again to download.");
  if (res.status === 403) {
    if ((await errorCode(res)) === "feature_disabled") throw new VaultError("disabled", "Downloads are turned off by the administrator.");
    throw new VaultError("forbidden", "You are not allowed to download this title.");
  }
  if (res.status === 404 || res.status === 410) throw new VaultError("gone", "This title is not available for download right now.");
  throw new VaultError(res.status >= 500 ? "network" : "protocol", `Download failed (${res.status}).`);
}

async function consume<T extends TransferState>(body: ReadableStream<Uint8Array>, holder: { state: T }, firstIndex: number, lastIndex: number, opts: TransferOptions<T>): Promise<void> {
  const { size, chunkSize } = holder.state;
  const reader = body.getReader();
  let index = firstIndex;
  let buffer = new Uint8Array(chunkLength(index, size, chunkSize));
  let filled = 0;
  try {
    while (index <= lastIndex) {
      let next: ReadableStreamReadResult<Uint8Array>;
      try {
        next = await reader.read();
      } catch (error) {
        if (opts.signal.aborted) throw abortError();
        throw new Retry(error instanceof Error ? error.message : "connection lost");
      }
      if (next.done) break;
      let bytes = next.value;
      while (bytes.byteLength > 0 && index <= lastIndex) {
        const take = Math.min(bytes.byteLength, buffer.byteLength - filled);
        buffer.set(bytes.subarray(0, take), filled);
        filled += take;
        bytes = bytes.subarray(take);
        if (filled === buffer.byteLength) {
          if (!holder.state.hashes[index]) {
            holder.state = await opts.sink.commit(holder.state, index, buffer);
            opts.onProgress?.(holder.state);
          }
          if (opts.signal.aborted) throw abortError();
          index++;
          filled = 0;
          buffer = new Uint8Array(chunkLength(index, size, chunkSize));
        }
      }
    }
  } finally {
    void reader.cancel().catch(() => {});
  }
  if (index <= lastIndex) throw new Retry("connection closed early");
}
