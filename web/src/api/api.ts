import type {
  ClientProfile,
  CreateInviteRequest,
  CreateInviteResponse,
  CreateShareRequest,
  CreateShareResponse,
  CreateUploadRequest,
  CreateUserRequest,
  DiscordSettings,
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
  getMe: () => request<Me>("/api/v1/me"),
  patchMe: (body: { display_name: string }) =>
    request<Me>("/api/v1/me", { method: "PATCH", body }),
  getPreferences: () => request<Preferences>("/api/v1/me/preferences"),
  putPreferences: (body: Preferences) =>
    request<Preferences>("/api/v1/me/preferences", { method: "PUT", body }),
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

  continueWatching: async () => asArray<ProgressRecord>(await request("/api/v1/playback/continue")),

  createSession: (body: {
    item_kind: "movie" | "episode";
    item_id: string;
    media_file_id?: string;
    start_ms?: number;
    quality?: string;
    audio_index?: number;
    subtitle_index?: number | null;
    client?: ClientProfile;
  }) =>
    request<PlaybackSession>("/api/v1/playback/sessions", {
      method: "POST",
      body: { ...body, client: body.client ?? detectClientProfile() },
    }),
  putProgress: (sessionId: string, body: ProgressPut) =>
    request(`/api/v1/playback/sessions/${sessionId}/progress`, { method: "PUT", body }),
  endSession: (sessionId: string) =>
    request(`/api/v1/playback/sessions/${sessionId}`, { method: "DELETE" }),

  getShare: (token: string) => request<ShareMeta>(`/api/v1/share/${token}`),
  unlockShare: (token: string, body: ShareUnlockRequest = {}) =>
    request<ShareUnlockResponse>(`/api/v1/share/${token}/unlock`, { method: "POST", body }),
  listShares: async () => asArray<ShareRow>(await request("/api/v1/shares")),
  createShare: (body: CreateShareRequest) =>
    request<CreateShareResponse>("/api/v1/shares", { method: "POST", body }),
  revokeShare: (id: string) => request(`/api/v1/shares/${id}`, { method: "DELETE" }),

  createUpload: (body: CreateUploadRequest) =>
    request<UploadSession>("/api/v1/uploads", { method: "POST", body }),
  uploadHead: (id: string) => head(`/api/v1/uploads/${id}`),
  uploadPut: (id: string, chunk: Blob, offset: number, total: number) =>
    request(`/api/v1/uploads/${id}`, {
      method: "PUT",
      body: chunk,
      headers: {
        "Content-Type": "application/octet-stream",
        "Upload-Offset": String(offset),
        "Content-Range": `bytes ${offset}-${offset + chunk.size - 1}/${total}`,
      },
    }),

  createWTRoom: (body: { item_kind: string; item_id: string }) =>
    request<WTRoom>("/api/v1/watch-together/rooms", { method: "POST", body }),
  getWTInvite: (code: string) => request<WTInvite>(`/api/v1/watch-together/invites/${code}`),
  joinWT: (body: WTJoinRequest) =>
    request<WTJoinResponse>("/api/v1/watch-together/join", { method: "POST", body }),
  wtTicket: (roomId: string) =>
    request<WTTicket>(`/api/v1/watch-together/rooms/${roomId}/ticket`, { method: "POST", body: {} }),

  adminStreams: async () => asArray<StreamRow>(await request("/api/v1/admin/streams")),
  adminInspector: (sessionId: string) =>
    request<Inspector>(`/api/v1/admin/streams/${sessionId}`),

  listUsers: async () => asArray<UserRow>(await request("/api/v1/users")),
  getUser: (id: string) => request<UserRow>(`/api/v1/users/${id}`),
  createUser: (body: CreateUserRequest) =>
    request<{ id: string; username: string }>("/api/v1/users", { method: "POST", body }),
  patchUser: (id: string, body: { display_name?: string; disabled?: boolean; role_ids?: string[]; password?: string }) =>
    request<UserRow>(`/api/v1/users/${id}`, { method: "PATCH", body }),
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
  getDiscordSettings: () => request<DiscordSettings>("/api/v1/admin/integrations/discord"),
  putDiscordSettings: (body: Partial<DiscordSettings> & { client_secret?: string }) =>
    request<DiscordSettings>("/api/v1/admin/integrations/discord", { method: "PUT", body }),

  getUpdates: () => request<UpdateStatus>("/api/v1/admin/updates"),
  putUpdates: (body: { auto_enabled: boolean }) =>
    request<UpdateStatus>("/api/v1/admin/updates", { method: "PUT", body }),
  checkUpdates: () => request<UpdateStatus>("/api/v1/admin/updates/check", { method: "POST", body: {} }),
  applyUpdates: () => request<{ ok: boolean; message?: string }>("/api/v1/admin/updates/apply", { method: "POST", body: {} }),
};

export { ApiError } from "./client";
