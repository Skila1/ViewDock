import type { ErrorBody } from "@/types/api.gen";

export class ApiError extends Error {
  status: number;
  code?: string;
  body?: unknown;

  constructor(status: number, message: string, code?: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

const CSRF_COOKIE = "vd_csrf";
const CSRF_HEADER = "X-CSRF-Token";

let csrfToken: string | null = null;
let csrfInflight: Promise<string | null> | null = null;

function readCsrfCookie(): string | null {
  if (typeof document === "undefined") return null;
  const parts = document.cookie.split(";");
  for (const part of parts) {
    const [k, ...rest] = part.trim().split("=");
    if (k === CSRF_COOKIE) return decodeURIComponent(rest.join("="));
  }
  return null;
}

export function clearCsrf(): void {
  csrfToken = null;
}

export async function ensureCsrf(): Promise<string | null> {
  const cookie = readCsrfCookie();
  if (cookie) {
    csrfToken = cookie;
    return cookie;
  }
  if (csrfToken) return csrfToken;
  if (!csrfInflight) {
    csrfInflight = fetch("/api/v1/auth/csrf", { credentials: "include" })
      .then(async (res) => {
        if (!res.ok) return readCsrfCookie();
        const json = (await res.json().catch(() => ({}))) as { token?: string };
        csrfToken = json.token ?? readCsrfCookie();
        return csrfToken;
      })
      .finally(() => {
        csrfInflight = null;
      });
  }
  return csrfInflight;
}

export type RequestOpts = {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  raw?: boolean;
};

async function parseBody(res: Response): Promise<unknown> {
  if (res.status === 204) return null;
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export async function request<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const method = (opts.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = { Accept: "application/json", ...opts.headers };
  const mutating = !["GET", "HEAD", "OPTIONS"].includes(method);

  if (mutating) {
    const token = (await ensureCsrf()) ?? readCsrfCookie();
    if (token) headers[CSRF_HEADER] = token;
  }

  let body: BodyInit | undefined;
  if (opts.body instanceof Blob || opts.body instanceof ArrayBuffer || opts.body instanceof FormData) {
    body = opts.body as BodyInit;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = headers["Content-Type"] ?? "application/json";
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(path, {
    method,
    credentials: "include",
    headers,
    body,
    signal: opts.signal,
  });

  if (res.status === 403 && mutating) {
    const maybe = (await res.clone().json().catch(() => null)) as ErrorBody | null;
    if (maybe?.code === "csrf") {
      clearCsrf();
      const retry = (await ensureCsrf()) ?? readCsrfCookie();
      if (retry && retry !== headers[CSRF_HEADER]) {
        return request<T>(path, opts);
      }
    }
  }

  if (opts.raw) {
    if (!res.ok) {
      throw new ApiError(res.status, res.statusText);
    }
    return res as T;
  }

  const parsed = await parseBody(res);
  if (!res.ok) {
    const err = parsed as ErrorBody | undefined;
    throw new ApiError(
      res.status,
      err?.message || res.statusText || "request failed",
      err?.code,
      parsed,
    );
  }
  return parsed as T;
}

export async function head(path: string): Promise<Headers> {
  const res = await fetch(path, { method: "HEAD", credentials: "include" });
  if (!res.ok) throw new ApiError(res.status, res.statusText);
  return res.headers;
}
