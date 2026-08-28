/** Hand-written types matching openapi/openapi.yaml plus inferred request/response bodies. */

export type ContentType = "movies" | "tv" | "mixed";
export type ItemKind = "movie" | "episode";
export type Delivery = "direct" | "hls";
export type HlsAttach = "native" | "mse";
export type PrincipalKind = "user" | "guest_share";

export interface ErrorBody {
  code: string;
  message: string;
}

export interface SystemInfo {
  name: string;
  version: string;
  api_version: string;
  tmdb_configured: boolean;
  setup_needed: boolean;
  media_dir?: string;
  discord_login?: boolean;
  discord_configured?: boolean;
  public_url?: string;
}

export interface CsrfResponse {
  token: string;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface Me {
  id: string;
  username: string;
  display_name: string;
  is_admin: boolean;
  kind: PrincipalKind;
  pin_locked: boolean;
  can_download?: boolean;
  has_password?: boolean;
  has_pin?: boolean;
  permissions?: string[];
  roles?: string[];
}

export interface SessionRow {
  id: string;
  ip: string;
  user_agent: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  current: boolean;
}

export interface IdentityRow {
  provider: string;
  provider_user_id: string;
  provider_username: string;
  avatar_hash?: string;
  linked_at: string;
}

export interface RoleRow {
  id: string;
  name: string;
  description?: string;
  is_system?: boolean;
  member_count?: number;
  permissions?: string[];
}

export interface PermissionRow {
  id: string;
  name: string;
  description: string;
}

export interface DiscordSettings {
  login_enabled: boolean;
  client_id: string;
  client_secret_set: boolean;
  registration_enabled: boolean;
  admin_discord_ids: string;
  redirect_uri: string;
}

export interface UpdateChangelogEntry {
  version: string;
  notes: string[];
}

export interface UpdateProgress {
  percent: number;
  stage: string;
  detail: string;
  log?: string;
}

export interface UpdateStatus {
  auto_enabled: boolean;
  helper_ok: boolean;
  socket_ok: boolean;
  can_apply: boolean;
  available: boolean;
  version: string;
  latest_version: string;
  image: string;
  current_digest?: string;
  latest_digest?: string;
  changelog?: UpdateChangelogEntry[];
  progress?: UpdateProgress | null;
  last_check_at?: string | null;
  last_applied_at?: string | null;
  last_status?: string;
  last_error?: string;
  last_applied_by?: string;
  checking?: boolean;
  updating?: boolean;
  apply_reason?: string;
}

export interface LibraryGrantUser {
  user_id: string;
  username: string;
  display_name: string;
  can_download: boolean;
}

export interface LibraryGrantRole {
  role_id: string;
  name: string;
  can_download: boolean;
}

export interface UserGrant {
  library_id: string;
  name: string;
  can_download: boolean;
}

export interface Preferences {
  audio_lang: string;
  subtitle_lang: string;
  subtitle_mode: string;
  autoplay: boolean;
}

export interface SetupStatus {
  needed: boolean;
  step: string;
  media_dir?: string;
  bootstrap_required?: boolean;
}

export interface SetupAdminRequest {
  username: string;
  password: string;
  display_name?: string;
  bootstrap_token?: string;
}

export interface SetupLibraryRequest {
  name: string;
  path: string;
  content_type: ContentType;
}

export interface SetupTmdbRequest {
  api_key?: string;
  skip: boolean;
}

export interface SetupScanRequest {
  library_id: string;
}

export interface SetupScanResponse {
  scan_run_id: string;
}

export interface DetectResult {
  ffmpeg: string;
  ffprobe: string;
  version: string;
  encoders: string[];
  filters: string[];
  hwaccel: string[];
  zscale: boolean;
}

export interface Library {
  id: string;
  name: string;
  path: string;
  content_type: ContentType;
  uploads_enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface Movie {
  id: string;
  title: string;
  year: number | null;
  poster_url: string | null;
  unmatched: boolean;
  metadata_source: string;
  overview?: string;
  library_id?: string;
  backdrop_url?: string | null;
}

export interface Series {
  id: string;
  title: string;
  year: number | null;
  poster_url: string | null;
  unmatched: boolean;
  metadata_source: string;
  overview?: string;
  library_id?: string;
  season_count?: number;
  episode_count?: number;
}

export interface Episode {
  id: string;
  series_id?: string;
  season: number;
  number: number;
  title: string;
  overview?: string;
  poster_url?: string | null;
  unmatched?: boolean;
  duration_ms?: number;
  intro_start_ms?: number | null;
  intro_end_ms?: number | null;
}

export interface Season {
  id?: string;
  number: number;
  title?: string;
  episodes: Episode[];
}

export interface MediaFile {
  id: string;
  filename?: string;
  rel_path?: string;
  duration_ms?: number;
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
}

export interface MovieDetail extends Movie {
  files?: MediaFile[];
  progress?: ProgressRecord | null;
}

export interface SeriesDetail extends Series {
  seasons: Season[];
  progress?: ProgressRecord | null;
}

export interface SearchHit {
  item_kind: "movie" | "series" | "episode";
  item_id: string;
  title: string;
  year?: number | null;
  poster_url?: string | null;
  unmatched?: boolean;
}

export interface SearchResponse {
  movies?: Movie[];
  series?: Series[];
  episodes?: Episode[];
  items?: SearchHit[];
}

export interface DecodingInfo {
  [key: string]: unknown;
}

export interface ClientProfile {
  user_agent: string;
  mse: boolean;
  hls_native: boolean;
  ass_js: boolean;
  hdr: boolean;
  viewport_w: number;
  viewport_h: number;
  hevc: boolean;
  av1: boolean;
  ac3: boolean;
  eac3: boolean;
  truehd: boolean;
  decoding_info?: DecodingInfo;
}

export interface CreateSession {
  item_kind: ItemKind;
  item_id: string;
  media_file_id?: string;
  start_ms?: number;
  quality?: string;
  audio_index?: number;
  subtitle_index?: number | null;
  client: ClientProfile;
}

export interface SessionTrack {
  index?: number;
  language?: string;
  title?: string;
  codec?: string;
  [key: string]: unknown;
}

export interface PlaybackDecision {
  reasons?: string[];
  mode?: string;
}

export interface PlaybackSession {
  id: string;
  delivery: Delivery;
  hls_attach?: HlsAttach;
  urls: Record<string, string>;
  qualities?: string[];
  audio?: SessionTrack[];
  subtitles?: SessionTrack[];
  decision?: PlaybackDecision;
  intro?: { start_ms?: number; end_ms?: number } | null;
  next_episode?: { id: string; title?: string } | null;
  duration_ms?: number;
  seekable_from_ms?: number;
}

export interface ProgressPut {
  position_ms: number;
  duration_ms: number;
}

export interface ProgressRecord {
  item_kind: ItemKind | string;
  item_id: string;
  media_file_id?: string;
  position_ms: number;
  duration_ms: number;
  completed?: boolean;
  resume_ms?: number;
  updated_at?: string;
  title?: string;
  poster_url?: string | null;
  unmatched?: boolean;
}

export interface ShareMeta {
  item_kind: ItemKind | string;
  item_id?: string;
  needs_password: boolean;
  title?: string;
  allow_download?: boolean;
}

export interface ShareUnlockRequest {
  password?: string;
}

export interface ShareUnlockResponse {
  ok: boolean;
  item_kind: ItemKind | string;
  item_id: string;
}

export interface CreateShareRequest {
  item_kind: ItemKind | string;
  item_id: string;
  password?: string;
  quality?: string;
  max_concurrent?: number;
  allow_download?: boolean;
  hours?: number;
}

export interface CreateShareResponse {
  id: string;
  token: string;
}

export interface ShareRow {
  id: string;
  item_kind: string;
  item_id: string;
  revoked?: boolean;
  created_at?: string;
}

export interface CreateUploadRequest {
  library_id: string;
  filename: string;
  size_bytes: number;
}

export interface UploadSession {
  id: string;
  offset_bytes?: number;
  library_id?: string;
  filename?: string;
  size_bytes?: number;
  status?: string;
}

export interface WTRoom {
  id: string;
  code?: string;
  invite_code?: string;
  item_kind: ItemKind | string;
  item_id: string;
  title?: string;
}

export interface WTInvite {
  code: string;
  room_id?: string;
  id?: string;
  item_kind: ItemKind | string;
  item_id: string;
  title?: string;
  host?: string;
}

export interface WTJoinRequest {
  code: string;
  display_name?: string;
}

export interface WTJoinResponse {
  room_id: string;
  role?: string;
  item_kind?: ItemKind | string;
  item_id?: string;
}

export interface WTTicket {
  ticket?: string;
  url?: string;
  ws_url?: string;
}

export interface StreamRow {
  id: string;
  session_id?: string;
  user?: string;
  username?: string;
  item_title?: string;
  title?: string;
  item_kind?: string;
  item_id?: string;
  delivery?: string;
  quality?: string;
  started_at?: string;
  client_ip?: string;
}

export interface InspectorSource {
  path?: string;
  filename?: string;
  container?: string;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
  duration_ms?: number;
  hdr?: string;
  gpu?: string | null;
  size_bytes?: number;
  [key: string]: unknown;
}

export interface InspectorClient extends Partial<ClientProfile> {
  [key: string]: unknown;
}

export interface InspectorDecision {
  mode?: string;
  delivery?: string;
  quality?: string;
  reasons?: string[];
  gpu?: string | null;
  [key: string]: unknown;
}

export interface Inspector {
  session_id?: string;
  source?: InspectorSource | null;
  client?: InspectorClient | null;
  decision?: InspectorDecision | null;
}

export interface UserRow {
  id: string;
  username: string;
  display_name: string;
  is_admin: boolean;
  disabled?: boolean;
  roles?: string[];
  role_ids?: string[];
  grants?: UserGrant[];
  discord_id?: string;
}

export interface CreateUserRequest {
  username: string;
  password: string;
  display_name?: string;
  admin?: boolean;
  role_ids?: string[];
}

export interface InviteRow {
  id: string;
  expires_at?: string;
  used?: boolean;
  is_admin?: boolean;
}

export interface CreateInviteRequest {
  days?: number;
  is_admin?: boolean;
  library_ids?: string[];
  can_download?: boolean;
}

export interface CreateInviteResponse {
  id: string;
  token: string;
}

export interface OkResponse {
  ok?: boolean;
}
