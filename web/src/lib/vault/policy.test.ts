import { describe, expect, it } from "vitest";
import { DEFAULT_OFFLINE_POLICY, type OfflinePolicy } from "@/api/vault";
import { checkAdmission, expiryFor, isExpired, libraryAllowed, planPolicy } from "./policy";

const DAY = 86_400_000;
const now = Date.parse("2026-09-27T00:00:00Z");
const policy = (patch: Partial<OfflinePolicy> = {}): OfflinePolicy => ({ ...DEFAULT_OFFLINE_POLICY, default_allowed: false, ...patch });

describe("offline policy", () => {
  it("resolves library permission with a default", () => {
    const p = policy({ libraries: { a: true, b: false } });
    expect(libraryAllowed(p, "a")).toBe(true);
    expect(libraryAllowed(p, "b")).toBe(false);
    expect(libraryAllowed(p, "c")).toBe(false);
    expect(libraryAllowed({ ...p, default_allowed: true }, "c")).toBe(true);
    expect(libraryAllowed({ ...p, enabled: false }, "a")).toBe(false);
  });

  it("computes expiry, with zero meaning never", () => {
    expect(expiryFor("2026-09-01T00:00:00.000Z", 30)).toBe("2026-10-01T00:00:00.000Z");
    expect(expiryFor("2026-09-01T00:00:00.000Z", 0)).toBeUndefined();
    expect(isExpired({ expiresAt: new Date(now - 1).toISOString() }, now)).toBe(true);
    expect(isExpired({}, now)).toBe(false);
  });

  it("expires, revokes and only ever tightens expiry", () => {
    const at = (days: number) => new Date(now - days * DAY).toISOString();
    const items = [
      { key: "expired", libraryId: "a", createdAt: at(40), downloadedAt: at(40), expiresAt: at(1) },
      { key: "revoked", libraryId: "b", createdAt: at(1), downloadedAt: at(1) },
      { key: "tighten", libraryId: "a", createdAt: at(5), downloadedAt: at(5), expiresAt: new Date(now + 60 * DAY).toISOString() },
      { key: "keep", libraryId: "a", createdAt: at(1), downloadedAt: at(1), expiresAt: new Date(now + 2 * DAY).toISOString() },
      { key: "lapsed", libraryId: "a", createdAt: at(20), downloadedAt: at(20) },
    ];
    const plan = planPolicy(items, policy({ libraries: { a: true, b: false }, expiry_days: 10 }), true, now);
    expect(plan.expired.sort()).toEqual(["expired", "lapsed"]);
    expect(plan.revoked).toEqual(["revoked"]);
    expect(plan.retime).toEqual([{ key: "tighten", expiresAt: new Date(now + 5 * DAY).toISOString() }]);
  });

  it("does not revoke from a cached or default policy", () => {
    const items = [{ key: "x", libraryId: "b", createdAt: new Date(now).toISOString() }];
    expect(planPolicy(items, policy({ libraries: { b: false } }), false, now).revoked).toEqual([]);
  });

  it("checks item limits, size caps and quota headroom", () => {
    const p = policy({ max_items: 2, max_item_bytes: 1000 });
    expect(checkAdmission({ size: 10, policy: p, itemCount: 2 })).toMatchObject({ ok: false, reason: "limit" });
    expect(checkAdmission({ size: 1001, policy: p, itemCount: 0 })).toMatchObject({ ok: false, reason: "too_large" });
    expect(checkAdmission({ size: 500, policy: p, itemCount: 0, estimate: { quota: 1000, usage: 450 } })).toMatchObject({ ok: false, reason: "quota" });
    expect(checkAdmission({ size: 500, policy: p, itemCount: 0, estimate: { quota: 1000, usage: 400 } })).toEqual({ ok: true, available: 500 });
    expect(checkAdmission({ size: 500, policy: p, itemCount: 0, alreadyStored: 100, estimate: { quota: 1000, usage: 450 } }).ok).toBe(true);
    expect(checkAdmission({ size: 500, policy: p, itemCount: 0, estimate: {} })).toEqual({ ok: true });
  });
});
