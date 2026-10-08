import type { OfflinePolicy } from "@/api/vault";

const DAY_MS = 24 * 60 * 60 * 1000;

export type PolicyItem = { key: string; libraryId?: string; downloadedAt?: string; createdAt: string; expiresAt?: string };

export function libraryAllowed(policy: OfflinePolicy, libraryId: string | undefined): boolean {
  if (!policy.enabled) return false;
  if (libraryId && libraryId in policy.libraries) return Boolean(policy.libraries[libraryId]);
  return policy.default_allowed;
}

/** expiryFor returns the ISO expiry for a download, or undefined when the policy never expires. */
export function expiryFor(downloadedAt: string, expiryDays: number): string | undefined {
  if (!expiryDays || expiryDays <= 0) return undefined;
  const at = Date.parse(downloadedAt);
  if (!Number.isFinite(at)) return undefined;
  return new Date(at + expiryDays * DAY_MS).toISOString();
}

export function isExpired(item: { expiresAt?: string }, now = Date.now()): boolean {
  if (!item.expiresAt) return false;
  const at = Date.parse(item.expiresAt);
  return Number.isFinite(at) && at <= now;
}

export type PolicyPlan = {
  /** Past their expiry: remove from the device. */
  expired: string[];
  /** Library download permission withdrawn: remove from the device. */
  revoked: string[];
  /** Expiry tightened by a shorter policy. */
  retime: { key: string; expiresAt: string }[];
};

/**
 * planPolicy decides what an authoritative server policy means for stored
 * items. Turning downloads off does not remove saved titles; withdrawing a
 * library's download grant does. Expiry only ever moves earlier, so a longer
 * policy never extends a download past what the user agreed to.
 */
export function planPolicy(items: readonly PolicyItem[], policy: OfflinePolicy | null, authoritative: boolean, now = Date.now()): PolicyPlan {
  const plan: PolicyPlan = { expired: [], revoked: [], retime: [] };
  for (const item of items) {
    if (isExpired(item, now)) {
      plan.expired.push(item.key);
      continue;
    }
    if (!policy || !authoritative) continue;
    const allowed = item.libraryId && item.libraryId in policy.libraries ? policy.libraries[item.libraryId] : policy.default_allowed;
    if (!allowed) {
      plan.revoked.push(item.key);
      continue;
    }
    const tightened = expiryFor(item.downloadedAt ?? item.createdAt, policy.expiry_days);
    if (!tightened) continue;
    if (!item.expiresAt || Date.parse(tightened) < Date.parse(item.expiresAt)) {
      if (Date.parse(tightened) <= now) plan.expired.push(item.key);
      else plan.retime.push({ key: item.key, expiresAt: tightened });
    }
  }
  return plan;
}

export type QuotaCheck = { ok: true; available?: number } | { ok: false; reason: "too_large" | "quota" | "limit"; message: string };

// Browsers evict or refuse writes well before usage reaches the quota.
export const QUOTA_HEADROOM = 0.9;

export function checkAdmission(input: { size: number; policy: OfflinePolicy; itemCount: number; alreadyStored?: number; estimate?: { usage?: number; quota?: number } }): QuotaCheck {
  const { size, policy } = input;
  if (input.itemCount >= policy.max_items) {
    return { ok: false, reason: "limit", message: `Your account can keep up to ${policy.max_items} offline titles on a device. Remove one to add another.` };
  }
  if (policy.max_item_bytes > 0 && size > policy.max_item_bytes) {
    return { ok: false, reason: "too_large", message: "This title is larger than the offline size limit set by the administrator." };
  }
  const quota = input.estimate?.quota;
  if (!quota) return { ok: true };
  const usage = input.estimate?.usage ?? 0;
  const available = Math.max(0, quota * QUOTA_HEADROOM - usage);
  const needed = Math.max(0, size - (input.alreadyStored ?? 0));
  if (needed > available) {
    return { ok: false, reason: "quota", message: "There is not enough browser storage on this device for this title. Remove offline titles or free up disk space." };
  }
  return { ok: true, available };
}
