import type {
  ClientProfile,
  ConfigHistoryEntry,
  ConfigState,
  CreateInviteRequest,
  CreateInviteResponse,
  CreateShareRequest,
  CreateShareResponse,
  CreateUploadRequest,
  CreateUserRequest,
  DiscordSettings,
  SiteSettings,
  DetectResult,
  IdentityRow,
  PermissionRow,
  RoleRow,
  SessionRow,
  Episode,
  Inspector,
  InviteRow,
  Library,
  LoginRequest,
  Me,
  Movie,
  MovieDetail,
  PlaybackSession,
  Preferences,
  ProgressPut,
  ProgressRecord,
  SearchResponse,
  Series,
  SeriesDetail,
  SetupAdminRequest,
  SetupLibraryRequest,
  SetupScanRequest,
  SetupScanResponse,
  SetupStatus,
  SetupTmdbRequest,
  ShareMeta,
  ShareRow,
  ShareUnlockRequest,
  ShareUnlockResponse,
  StreamRow,
  SystemInfo,
  UpdateStatus,
  UploadSession,
  UserGrant,
  UserRow,
  WTInvite,
  WTJoinRequest,
  WTJoinResponse,
  WTRoom,
  WTTicket,
} from "@/types/api.gen";
import { asArray } from "@/lib/asArray";
import { clearCsrf, ensureCsrf, head, request } from "./client";
import { detectClientProfile } from "./profile";

export type ActivityRoom = { room_id: string; invite_code: string; title: string; created?: boolean };

// Per-session calls go to the media worker that owns the session. Workers
// authenticate these with the session stream token, so no cookie is required.
const sessionBases = new Map<string, { base: string; stoken?: string }>();

function rememberSession(sess: PlaybackSession): PlaybackSession {
  const base = sess.urls?.session;
  if (base) sessionBases.set(sess.id, { base, stoken: sess.stoken });
  return sess;
}

function forgetSession(id: string) {
  sessionBases.delete(id);
}

export function sessionPath(id: string, suffix = ""): string {
  const known = sessionBases.get(id);
  if (!known) return `/api/v1/playback/sessions/${id}${suffix}`;
  const query = known.stoken ? `?stoken=${encodeURIComponent(known.stoken)}` : "";
  return `${known.base}${suffix}${query}`;
}

export const api = {
  ensureCsrf,
  detectClientProfile,

  getSystem: () => request<SystemInfo>("/api/v1/system"),
  getCsrf: () => request<{ token: string }>("/api/v1/auth/csrf"),
  login: async (body: LoginRequest) => {
    const me = await request<Me>("/api/v1/auth/login", { method: "POST", body });
    clearCsrf();
    await ensureCsrf();
    return me;
  },
  logout: async () => {
    await request("/api/v1/auth/logout", { method: "POST", body: {} });
    clearCsrf();
  },
  getActivityConfig: () =>
    request<{ enabled: boolean; client_id?: string; scopes?: string[] }>("/api/v1/auth/discord/activity"),
  activitySignIn: async (code: string) => {
    const out = await request<{ access_token: string; user: Me }>("/api/v1/auth/discord/activity", {
      method: "POST",
      body: { code },
    });
    clearCsrf();
    await ensureCsrf();
    return out;
  },
  activityRoom: (body: { instance_id: string; item_kind?: "movie" | "episode"; item_id?: string }) =>
    request<{ room: ActivityRoom | null; can_create?: boolean }>("/api/v1/discord/activity/room", {
      method: "POST",
      body,
    }),
  getMe: () => request<Me>("/api/v1/me"),
  patchMe: (body: { display_name: string }) =>
    request<Me>("/api/v1/me", { method: "PATCH", body, queueWhenOffline: true }),
  getPreferences: () => request<Preferences>("/api/v1/me/preferences"),
  putPreferences: (body: Preferences) =>
    request<Preferences>("/api/v1/me/preferences", { method: "PUT", body, queueWhenOffline: true }),
  changePassword: (body: { current?: string; next: string }) =>
    request("/api/v1/me/password", { method: "POST", body }),
  setPin: (pin: string) => request("/api/v1/me/pin", { method: "POST", body: { pin } }),
  clearPin: () => request("/api/v1/me/pin", { method: "DELETE" }),
  unlockPin: (pin: string) => request("/api/v1/me/pin/unlock", { method: "POST", body: { pin } }),
  listSessions: async () => asArray<SessionRow>(await request("/api/v1/me/sessions")),
  revokeSession: (id: string) => request(`/api/v1/me/sessions/${id}`, { method: "DELETE" }),
  listIdentities: async () => asArray<IdentityRow>(await request("/api/v1/me/identities")),
  unlinkDiscord: () => request("/api/v1/me/identities/discord", { method: "DELETE" }),

  setupStatus: () => request<SetupStatus>("/api/v1/setup/status"),
  setupAdmin: (body: SetupAdminRequest) =>
    request<{ id: string; username: string }>("/api/v1/setup/admin", { method: "POST", body }),
  setupLibrary: (body: SetupLibraryRequest) =>
    request<Library>("/api/v1/setup/library", { method: "POST", body }),
  setupFfmpeg: () => request<DetectResult>("/api/v1/setup/ffmpeg"),
  setupTmdb: (body: SetupTmdbRequest) => request("/api/v1/setup/tmdb", { method: "POST", body }),
  setupScan: (body: SetupScanRequest) =>
    request<SetupScanResponse>("/api/v1/setup/scan", { method: "POST", body }),
  setupComplete: () => request("/api/v1/setup/complete", { method: "POST", body: {} }),

  listLibraries: async () => asArray<Library>(await request("/api/v1/libraries")),
  createLibrary: (body: SetupLibraryRequest & { uploads_enabled?: boolean }) =>
    request<Library>("/api/v1/libraries", { method: "POST", body }),
  patchLibrary: (id: string, body: Partial<Library>) =>
    request<Library>(`/api/v1/libraries/${id}`, { method: "PATCH", body }),
  scanLibrary: (id: string) => request(`/api/v1/libraries/${id}/scan`, { method: "POST", body: {} }),

  listMovies: async () => asArray<Movie>(await request("/api/v1/movies")),
  getMovie: (id: string) => request<MovieDetail>(`/api/v1/movies/${id}`),
  listSeries: async () => asArray<Series>(await request("/api/v1/series")),
  getSeries: (id: string) => request<SeriesDetail>(`/api/v1/series/${id}`),
  getEpisode: (id: string) => request<Episode>(`/api/v1/episodes/${id}`),
  nextEpisode: (seriesId: string) => request<Episode>(`/api/v1/series/${seriesId}/next`),

  search: (q: string) =>
    request<SearchResponse>(`/api/v1/search?q=${encodeURIComponent(q)}`),
  smartSearch: (q: string) =>
    request<SearchResponse>(`/api/v1/search/smart?q=${encodeURIComponent(q)}`),

  continueWatching: async () => asArray<ProgressRecord>(await request("/api/v1/playback/continue")),

  createSession: async (body: {
    item_kind: "movie" | "episode";
    item_id: string;
    media_file_id?: string;
    start_ms?: number;
    quality?: string;
    audio_index?: number;
    subtitle_index?: number | null;
    client?: ClientProfile;
    replace_session_id?: string;
    source?: string;
  }) =>
    rememberSession(
      await request<PlaybackSession>("/api/v1/playback/sessions", {
        method: "POST",
        body: { ...body, client: body.client ?? (await detectClientProfile()) },
      }),
    ),
  putProgress: (
    sessionId: string,
    body: ProgressPut & { event?: "pause" | "seek" | "ended" | "stop" },
    item?: { kind: string; id: string },
  ) =>
    request(sessionPath(sessionId, "/progress"), {
      method: "PUT",
      body,
      offlineFallback: item
        ? {
            path: `/api/v1/progress/${item.kind}/${encodeURIComponent(item.id)}`,
            method: "PUT",
            body: { position_ms: body.position_ms, duration_ms: body.duration_ms, client_updated_at: new Date().toISOString() },
          }
        : undefined,
    }),
  putItemProgress: (kind: string, id: string, body: { position_ms: number; duration_ms: number; client_updated_at: string }) =>
    request(`/api/v1/progress/${kind}/${encodeURIComponent(id)}`, { method: "PUT", body, queueWhenOffline: true }),
  endSession: (sessionId: string) => request(sessionPath(sessionId), { method: "DELETE" }).finally(() => forgetSession(sessionId)),
  sessionTelemetry: (sessionId: string, events: { type: string; at: number; data?: Record<string, unknown> }[]) =>
    request(sessionPath(sessionId, "/telemetry"), { method: "POST", body: { events } }),

  getShare: (token: string) => request<ShareMeta>(`/api/v1/share/${token}`),
  unlockShare: (token: string, body: ShareUnlockRequest = {}) =>
    request<ShareUnlockResponse>(`/api/v1/share/${token}/unlock`, { method: "POST", body }),
  listShares: async () => asArray<ShareRow>(await request("/api/v1/shares")),
  createShare: (body: CreateShareRequest) =>
    request<CreateShareResponse>("/api/v1/shares", { method: "POST", body }),
  revokeShare: (id: string) => request(`/api/v1/shares/${id}`, { method: "DELETE" }),

  createUpload: (body: CreateUploadRequest) =>
    request<UploadSession>("/api/v1/uploads", { method: "POST", body }),
  listUploads: async () => asArray<UploadSession>(await request("/api/v1/uploads")),
  getUpload: (id: string) => request<UploadSession>(`/api/v1/uploads/${id}`),
  uploadHead: (id: string) => head(`/api/v1/uploads/${id}`),
  uploadPut: (id: string, chunk: Blob, offset: number, total: number) =>
    request<UploadSession>(`/api/v1/uploads/${id}`, {
      method: "PUT",
      body: chunk,
      headers: {
        "Content-Type": "application/octet-stream",
        "Upload-Offset": String(offset),
        "Content-Range": `bytes ${offset}-${offset + chunk.size - 1}/${total}`,
      },
    }),
  cancelUpload: (id: string) => request(`/api/v1/uploads/${id}`, { method: "DELETE" }),

  createWTRoom: (body: { item_kind: string; item_id: string }) =>
    request<WTRoom>("/api/v1/watch-together/rooms", { method: "POST", body }),
  getWTInvite: (code: string) => request<WTInvite>(`/api/v1/watch-together/invites/${code}`),
  joinWT: (body: WTJoinRequest) =>
    request<WTJoinResponse>("/api/v1/watch-together/join", { method: "POST", body }),
  wtTicket: (roomId: string) =>
    request<WTTicket>(`/api/v1/watch-together/rooms/${roomId}/ticket`, { method: "POST", body: {} }),
  addCinemaQueue: (roomId: string, body: { item_kind: string; item_id: string; title?: string }) =>
    request(`/api/v1/watch-together/rooms/${roomId}/queue`, { method: "POST", body }),
  voteCinemaNext: (roomId: string, itemId: string) =>
    request(`/api/v1/watch-together/rooms/${roomId}/vote`, { method: "POST", body: { item_id: itemId } }),
  setCinemaIntermission: (roomId: string, seconds: number) =>
    request(`/api/v1/watch-together/rooms/${roomId}/intermission`, { method: "POST", body: { seconds } }),

  adminStreams: async () => asArray<StreamRow>(await request("/api/v1/admin/streams")),
  adminInspector: (sessionId: string) =>
    request<Inspector>(`/api/v1/admin/streams/${sessionId}`),
  adminFlightRecorder: (sessionId: string) =>
    request<{ at: string; session_id: string; type: string; data?: Record<string, unknown> }[]>(`/api/v1/admin/diagnostics/flight-recorder/${sessionId}`),
  adminSourceReliability: () =>
    request<{ source: string; attempts: number; successes: number; failures: number; stalls: number; success_rate: number; average_ms: number }[]>("/api/v1/admin/diagnostics/sources"),

  listUsers: async () => asArray<UserRow>(await request("/api/v1/users")),
  getUser: (id: string) => request<UserRow>(`/api/v1/users/${id}`),
  createUser: (body: CreateUserRequest) =>
    request<{ id: string; username: string }>("/api/v1/users", { method: "POST", body }),
  patchUser: (id: string, body: { display_name?: string; disabled?: boolean; role_ids?: string[]; password?: string }) =>
    request<UserRow>(`/api/v1/users/${id}`, { method: "PATCH", body }),
  deleteUser: (id: string) => request(`/api/v1/users/${id}`, { method: "DELETE" }),
  setUserGrant: (id: string, body: { library_id: string; can_download: boolean }) =>
    request(`/api/v1/users/${id}/grants`, { method: "POST", body }),
  deleteUserGrant: (id: string, libraryId: string) =>
    request(`/api/v1/users/${id}/grants?library_id=${encodeURIComponent(libraryId)}`, { method: "DELETE" }),
  listUserGrants: async (id: string) => asArray<UserGrant>(await request(`/api/v1/users/${id}/grants`)),
  listInvites: async () => asArray<InviteRow>(await request("/api/v1/invites")),
  createInvite: (body: CreateInviteRequest) =>
    request<CreateInviteResponse>("/api/v1/invites", { method: "POST", body }),

  listRoles: async () => asArray<RoleRow>(await request("/api/v1/admin/roles")),
  listPermissions: async () => asArray<PermissionRow>(await request("/api/v1/admin/permissions")),
  createRole: (body: { name: string; description?: string; permissions?: string[] }) =>
    request<RoleRow>("/api/v1/admin/roles", { method: "POST", body }),
  patchRole: (id: string, body: { description?: string; permissions?: string[] }) =>
    request<RoleRow>(`/api/v1/admin/roles/${id}`, { method: "PATCH", body }),
  deleteRole: (id: string) => request(`/api/v1/admin/roles/${id}`, { method: "DELETE" }),
  addRoleMembers: (id: string, userIds: string[]) =>
    request(`/api/v1/admin/roles/${id}/members`, { method: "POST", body: { user_ids: userIds } }),
  getRole: (id: string) =>
    request<{ role: RoleRow; members: { id: string; username: string; display_name: string }[] }>(`/api/v1/admin/roles/${id}`),
  removeRoleMember: (id: string, userId: string) =>
    request(`/api/v1/admin/roles/${id}/members/${encodeURIComponent(userId)}`, { method: "DELETE" }),
  listLibraryGrants: (libraryId: string) =>
    request<{ users: import("@/types/api.gen").LibraryGrantUser[]; roles: import("@/types/api.gen").LibraryGrantRole[] }>(
      `/api/v1/admin/libraries/${libraryId}/grants`,
    ),
  setLibraryGrant: (libraryId: string, body: { user_id?: string; role_id?: string; can_download: boolean }) =>
    request(`/api/v1/admin/libraries/${libraryId}/grants`, { method: "POST", body }),
  deleteLibraryGrant: (libraryId: string, q: { user_id?: string; role_id?: string }) => {
    const p = new URLSearchParams();
    if (q.user_id) p.set("user_id", q.user_id);
    if (q.role_id) p.set("role_id", q.role_id);
    return request(`/api/v1/admin/libraries/${libraryId}/grants?${p.toString()}`, { method: "DELETE" });
  },
  getSiteSettings: () => request<SiteSettings>("/api/v1/admin/settings"),
  putSiteSettings: (body: { public_url?: string; tmdb_api_key?: string }) =>
    request<SiteSettings>("/api/v1/admin/settings", { method: "PUT", body }),
  getConfig: () => request<ConfigState>("/api/v1/admin/config"),
  putConfig: (body: { version: number; values: Record<string, string | null>; note?: string }) =>
    request<ConfigState>("/api/v1/admin/config", { method: "PUT", body }),
  getConfigHistory: () => request<{ items: ConfigHistoryEntry[] }>("/api/v1/admin/config/history?limit=100"),
  rollbackConfig: (body: { target_version: number; version: number }) =>
    request<ConfigState>("/api/v1/admin/config/rollback", { method: "POST", body }),
  getDiscordSettings: () => request<DiscordSettings>("/api/v1/admin/integrations/discord"),
  putDiscordSettings: (body: Partial<DiscordSettings> & { client_secret?: string }) =>
    request<DiscordSettings>("/api/v1/admin/integrations/discord", { method: "PUT", body }),
  sendDiscordBotInvite: (body: { channel_id: string; invite_url: string; title?: string }) =>
    request("/api/v1/admin/integrations/discord/bot/invite", { method: "POST", body }),

  getUpdates: () => request<UpdateStatus>("/api/v1/admin/updates"),
  putUpdates: (body: { auto_enabled: boolean }) =>
    request<UpdateStatus>("/api/v1/admin/updates", { method: "PUT", body }),
  checkUpdates: () => request<UpdateStatus>("/api/v1/admin/updates/check", { method: "POST", body: {} }),
  applyUpdates: () => request<{ ok: boolean; message?: string }>("/api/v1/admin/updates/apply", { method: "POST", body: {} }),

  listAPIKeys: async () => asArray<APIKeyRow>(await request("/api/v1/admin/api-keys")),
  listAPIKeyScopes: async () => asArray<APIKeyScope>(await request("/api/v1/admin/api-keys/scopes")),
  createAPIKey: (body: { name: string; scopes: string[] }) =>
    request<APIKeyRow>("/api/v1/admin/api-keys", { method: "POST", body }),
  revokeAPIKey: (id: string) => request(`/api/v1/admin/api-keys/${id}`, { method: "DELETE" }),
  listMediaSources: async () => asArray<MediaSource>(await request("/api/v1/admin/media-sources")),
  createMediaSource: (body: MediaSourceInput) =>
    request<MediaSource>("/api/v1/admin/media-sources", { method: "POST", body }),
  updateMediaSource: (id: string, body: Partial<MediaSourceInput> & { enabled?: boolean }) =>
    request<MediaSource>(`/api/v1/admin/media-sources/${encodeURIComponent(id)}`, { method: "PATCH", body }),
  deleteMediaSource: (id: string) =>
    request(`/api/v1/admin/media-sources/${encodeURIComponent(id)}`, { method: "DELETE" }),
  testMediaSource: (body: { id?: string; url?: string; username?: string; password?: string }) =>
    request<MediaSourceTest>("/api/v1/admin/media-sources/test", { method: "POST", body }),
  syncMediaSource: (id: string) =>
    request(`/api/v1/admin/media-sources/${encodeURIComponent(id)}/sync`, { method: "POST", body: {} }),
  listNodes: async () => asArray<BackendNode>(await request("/api/v1/admin/nodes")),
  saveNode: (body: BackendNodeInput) => request<BackendNode>("/api/v1/admin/nodes", { method: "POST", body }),
  deleteNode: (id: string) => request(`/api/v1/admin/nodes/${encodeURIComponent(id)}`, { method: "DELETE" }),
  drainNode: (id: string, draining: boolean) =>
    request<BackendNode>(`/api/v1/admin/nodes/${encodeURIComponent(id)}/drain`, { method: "PATCH", body: { draining } }),
  probeNode: (id: string) =>
    request<BackendNode>(`/api/v1/admin/nodes/${encodeURIComponent(id)}/probe`, { method: "POST", body: {} }),
  issueNodeCredential: (id: string) =>
    request<{ id: string; secret: string; env: string }>(`/api/v1/admin/nodes/${encodeURIComponent(id)}/credential`, {
      method: "POST",
      body: {},
    }),
  reportClientLogs: (body: { events: { name: string; t?: number; details?: Record<string, unknown> }[] }) =>
    request<{ ok: boolean; accepted?: number }>("/api/v1/client-logs", { method: "POST", body }),
  listLogs: (q: { level?: string; category?: string; q?: string; limit?: number; after?: string } = {}) => {
    const p = new URLSearchParams();
    if (q.level) p.set("level", q.level);
    if (q.category) p.set("category", q.category);
    if (q.q) p.set("q", q.q);
    if (q.limit) p.set("limit", String(q.limit));
    if (q.after) p.set("after", q.after);
    return request<{ items: LogRow[]; next?: string }>(`/api/v1/admin/logs?${p.toString()}`);
  },
};

export type APIKeyRow = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  created_at: string;
  last_used_at?: string;
  secret?: string;
  note?: string;
};

export type APIKeyScope = { name: string; description: string };

export type BackendNodeRole = "media-worker" | "transcode-worker" | "storage-worker";

export type BackendNodeInput = {
  id?: string;
  name: string;
  host: string;
  port: number;
  scheme: "http" | "https";
  role: BackendNodeRole;
  region: string;
  capabilities: string;
  priority: number;
  weight: number;
  capacity: number;
  enabled: boolean;
  draining: boolean;
};

export type MediaSourceInput = {
  name?: string;
  url: string;
  username: string;
  password?: string;
  libraries?: string[];
  enabled?: boolean;
};

/** A connected Jellyfin server. Credentials are never returned. */
export type MediaSource = {
  id: string;
  library_id: string;
  name: string;
  url: string;
  username: string;
  libraries: string[];
  enabled: boolean;
  status: "pending" | "syncing" | "ok" | "error" | string;
  last_error: string;
  last_sync_at: string;
  item_count: number;
  syncing: boolean;
};

export type MediaSourceLibrary = { id: string; name: string; collection_type: string };
export type MediaSourceTest = { server_name: string; version: string; libraries: MediaSourceLibrary[] };

export type BackendNode = BackendNodeInput & {
  id: string;
  status: string;
  latency_ms: number;
  health: string;
  updated_at: string;
  created_at: string;
  has_credential: boolean;
  failures: number;
  last_failure_at: string;
  last_error: string;
};

export type LogRow = {
  id: string;
  created_at: string;
  level: string;
  category: string;
  message: string;
  details?: Record<string, unknown>;
  actor_id?: string;
};

export { ApiError } from "./client";
