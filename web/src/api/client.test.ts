import { afterEach, describe, expect, it, vi } from "vitest";
import { clearCsrf, ensureCsrf, request } from "./client";

describe("api client csrf", () => {
  afterEach(() => {
    clearCsrf();
    vi.unstubAllGlobals();
    document.cookie = "vd_csrf=; Max-Age=0; path=/";
  });

  it("sends credentials and X-CSRF-Token on mutations", async () => {
    document.cookie = "vd_csrf=tok123; path=/";
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => JSON.stringify({ ok: true }),
      clone: () => ({ json: async () => ({}) }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await request("/api/v1/auth/logout", { method: "POST", body: {} });

    expect(fetchMock).toHaveBeenCalled();
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.credentials).toBe("include");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toBe("tok123");
  });

  it("loads csrf from GET /api/v1/auth/csrf", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ token: "from-api" }),
    });
    vi.stubGlobal("fetch", fetchMock);
    const token = await ensureCsrf();
    expect(token).toBe("from-api");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/auth/csrf");
  });
});
