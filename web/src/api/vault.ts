import { ApiError, request } from "./client";

export type OfflinePolicy = {
  enabled: boolean;
  max_items: number;
  expiry_days: number;
  max_item_bytes: number;
  default_allowed: boolean;
  libraries: Record<string, boolean>;
  issued_at?: string;
};

export type PolicySource = "server" | "cached" | "default";

// Used when the server predates the offline policy endpoint. The download
// endpoint still enforces library grants, so allowing by default is safe.
export const DEFAULT_OFFLINE_POLICY: OfflinePolicy = {
  enabled: true,
  max_items: 10,
  expiry_days: 30,
  max_item_bytes: 0,
  default_allowed: true,
  libraries: {},
};

export const SPEED_TEST_PATH = "/api/v1/offline/speedtest";
const POLICY_PATH = "/api/v1/offline/policy";

function cacheKey(userId: string) {
  return `viewdock:offline-policy:${userId}`;
}

function normalize(raw: Partial<OfflinePolicy> | null | undefined): OfflinePolicy {
  return {
    enabled: raw?.enabled ?? DEFAULT_OFFLINE_POLICY.enabled,
    max_items: Math.max(1, Number(raw?.max_items) || DEFAULT_OFFLINE_POLICY.max_items),
    expiry_days: Math.max(0, Number(raw?.expiry_days ?? DEFAULT_OFFLINE_POLICY.expiry_days) || 0),
    max_item_bytes: Math.max(0, Number(raw?.max_item_bytes) || 0),
    default_allowed: raw?.default_allowed ?? DEFAULT_OFFLINE_POLICY.default_allowed,
    libraries: raw?.libraries && typeof raw.libraries === "object" ? raw.libraries : {},
    issued_at: raw?.issued_at,
  };
}

/**
 * getOfflinePolicy fetches the account's vault policy and keeps the last good
 * copy per user, so expiry and limits still apply while offline.
 */
export async function getOfflinePolicy(userId: string): Promise<{ policy: OfflinePolicy; source: PolicySource }> {
  try {
    const policy = normalize(await request<OfflinePolicy>(POLICY_PATH));
    try {
      localStorage.setItem(cacheKey(userId), JSON.stringify(policy));
    } catch {
      // Storage full or disabled; the live policy still applies this session.
    }
    return { policy, source: "server" };
  } catch (error) {
    if (error instanceof ApiError && (error.status === 404 || error.status === 405)) {
      return { policy: DEFAULT_OFFLINE_POLICY, source: "default" };
    }
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) throw error;
    const cached = readCachedPolicy(userId);
    if (cached) return { policy: cached, source: "cached" };
    return { policy: DEFAULT_OFFLINE_POLICY, source: "default" };
  }
}

export function readCachedPolicy(userId: string): OfflinePolicy | null {
  try {
    const raw = localStorage.getItem(cacheKey(userId));
    return raw ? normalize(JSON.parse(raw) as Partial<OfflinePolicy>) : null;
  } catch {
    return null;
  }
}

export function forgetCachedPolicy(userId: string) {
  try {
    localStorage.removeItem(cacheKey(userId));
  } catch {
    // Nothing to remove when storage is unavailable.
  }
}
