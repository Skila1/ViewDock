import { request } from "./client";

export type HouseholdRole = "owner" | "adult" | "member" | "child";
export type AssignableHouseholdRole = Exclude<HouseholdRole, "owner">;

export type Household = {
  id: string;
  name: string;
  owner_id: string;
  created_at: string;
  owner_username?: string;
  member_count?: number;
};

export type HouseholdMember = {
  id: string;
  username: string;
  display_name: string;
  role: HouseholdRole;
  age_limit: number;
  expires_at: string | null;
  joined_at?: string;
  temporary?: boolean;
};

export type HouseholdView = { household: Household | null; members: HouseholdMember[] };
export type AdminHouseholdView = { household: Household; members: HouseholdMember[] };
export type HouseholdInvite = { id: string; token: string; expires_at: string };

export type ContentRestriction = {
  max_age: number;
  user_limit: number;
  household_limit: number;
  block_unrated: boolean;
};

/** Rating fields returned on movie and series payloads. */
export type RatedTitle = {
  content_rating?: string;
  rating_age?: number | null;
  rating_source?: string;
};

export const MAX_RATING_AGE = 21;

/** Viewer restriction presets: a viewer sees titles rated at or below the age. */
export const AGE_LIMIT_PRESETS: { value: number; label: string }[] = [
  { value: 0, label: "No restriction" },
  { value: 7, label: "Up to 7 (G, TV-Y7)" },
  { value: 10, label: "Up to 10 (PG, TV-PG)" },
  { value: 13, label: "Up to 13 (PG-13)" },
  { value: 14, label: "Up to 14 (TV-14)" },
  { value: 16, label: "Up to 16" },
  { value: 17, label: "Up to 17 (R, TV-MA)" },
  { value: 18, label: "Up to 18 (NC-17)" },
];

export function ageLimitOptions(current: number): { value: number; label: string }[] {
  if (AGE_LIMIT_PRESETS.some((p) => p.value === current)) return AGE_LIMIT_PRESETS;
  return [...AGE_LIMIT_PRESETS, { value: current, label: `Up to ${current}` }].sort((a, b) => a.value - b.value);
}

export function ageLimitLabel(age: number): string {
  if (!age) return "No restriction";
  return AGE_LIMIT_PRESETS.find((p) => p.value === age)?.label ?? `Up to ${age}`;
}

/** Title certifications an administrator can pick; the server maps them to ages. */
export const TITLE_RATINGS = ["G", "PG", "PG-13", "R", "NC-17", "TV-Y", "TV-Y7", "TV-G", "TV-PG", "TV-14", "TV-MA"];

const json = (method: string, body?: unknown) => ({ method, body });
const enc = encodeURIComponent;

export const households = {
  mine: () => request<HouseholdView>("/api/v1/household"),
  create: (name: string) => request<HouseholdView>("/api/v1/household", json("POST", { name })),
  updateMember: (body: { user_id: string; role: AssignableHouseholdRole; age_limit: number }) =>
    request<HouseholdView>("/api/v1/household/members", json("POST", body)),
  removeMember: (userId: string) => request<{ ok: boolean }>(`/api/v1/household/members/${enc(userId)}`, json("DELETE")),
  createInvite: (body: { days?: number; role: AssignableHouseholdRole; age_limit: number }) =>
    request<HouseholdInvite>("/api/v1/household/invites", json("POST", body)),
  acceptInvite: (token: string) => request<HouseholdView>("/api/v1/household/invites/accept", json("POST", { token })),
  myRestriction: () => request<ContentRestriction>("/api/v1/content-restriction"),
};

export const adminHouseholds = {
  list: async () => (await request<{ items: Household[] }>("/api/v1/admin/households")).items ?? [],
  get: (id: string) => request<AdminHouseholdView>(`/api/v1/admin/households/${enc(id)}`),
  create: (body: { name: string; owner_id: string }) =>
    request<AdminHouseholdView>("/api/v1/admin/households", json("POST", body)),
  update: (id: string, body: { name?: string; owner_id?: string }) =>
    request<AdminHouseholdView>(`/api/v1/admin/households/${enc(id)}`, json("PATCH", body)),
  remove: (id: string) => request<{ ok: boolean }>(`/api/v1/admin/households/${enc(id)}`, json("DELETE")),
  createInvite: (id: string, body: { days?: number; role: AssignableHouseholdRole; age_limit: number }) =>
    request<HouseholdInvite>(`/api/v1/admin/households/${enc(id)}/invites`, json("POST", body)),
  updateMember: (id: string, userId: string, body: { role?: AssignableHouseholdRole; age_limit?: number }) =>
    request<AdminHouseholdView>(`/api/v1/admin/households/${enc(id)}/members/${enc(userId)}`, json("PATCH", body)),
  moveMember: (id: string, userId: string, householdId: string) =>
    request<AdminHouseholdView>(`/api/v1/admin/households/${enc(id)}/members/${enc(userId)}/move`, json("POST", { household_id: householdId })),
  removeMember: (id: string, userId: string) =>
    request<AdminHouseholdView>(`/api/v1/admin/households/${enc(id)}/members/${enc(userId)}`, json("DELETE")),
};

export const contentRatings = {
  setUserLimit: (userId: string, limit: number) =>
    request<{ content_age_limit: number; content_restriction?: ContentRestriction }>(`/api/v1/users/${enc(userId)}`, json("PATCH", { content_age_limit: limit })),
  setTitle: (kind: "movie" | "series", id: string, body: { content_rating: string; rating_age?: number | null }) =>
    request<RatedTitle>(`/api/v1/${kind === "movie" ? "movies" : "series"}/${enc(id)}/rating`, json("PUT", body)),
  resetTitle: (kind: "movie" | "series", id: string) =>
    request<{ ok: boolean }>(`/api/v1/${kind === "movie" ? "movies" : "series"}/${enc(id)}/rating`, json("DELETE")),
};
