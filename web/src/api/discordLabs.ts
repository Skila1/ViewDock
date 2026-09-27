import { request } from "./client";

export type DiscordChannelLink = {
  channel_id: string;
  guild_id: string;
  kind: "text" | "voice";
  room_id: string;
  invite_code: string;
  title: string;
  linked_by: string;
  discord_user_id: string;
  created_at: string;
  updated_at: string;
  room_active: boolean;
};

export type DiscordCommandRegistration = {
  application_id: string;
  guild_id?: string;
  commands: number;
  registered_at: string;
  registered_by: string;
};

export type DiscordInteractionsStatus = {
  endpoint_url: string;
  endpoint_https: boolean;
  public_key_set: boolean;
  public_key_valid: boolean;
  bot_configured: boolean;
  parties_enabled: boolean;
  registration: DiscordCommandRegistration | null;
  commands: string[];
  links: DiscordChannelLink[];
};

export type DiscordRegisterResult = {
  registration: DiscordCommandRegistration;
  public_key_status: "ok" | "saved" | "unset" | "mismatch" | "unknown";
  endpoint_url: string;
};

export type LabsMode = "v4l2" | "rtmp" | "srt";

export type LabsSelection = { room_id?: string; item_kind?: string; item_id?: string };

export type LabsConfig = {
  mode: LabsMode;
  device?: string;
  width: number;
  height: number;
  fps: number;
  video_kbps: number;
  audio_kbps: number;
  threads: number;
  selection: LabsSelection;
  output_url_set: boolean;
  output_display?: string;
  updated_at?: string;
  updated_by?: string;
};

export type LabsConfigInput = Omit<LabsConfig, "output_url_set" | "output_display" | "updated_at" | "updated_by"> & {
  // Omit to keep the saved URL; an empty string clears it.
  output_url?: string;
};

export type LabsModeStatus = { available: boolean; reason?: string };

export type LabsDevice = { path: string; name: string; virtual: boolean };

export type LabsCapabilities = {
  platform: string;
  ffmpeg: boolean;
  ffmpeg_version?: string;
  v4l2loopback: boolean;
  devices: LabsDevice[];
  modes: Record<LabsMode, LabsModeStatus>;
  checked_at: string;
};

export type LabsState = "disabled" | "stopped" | "starting" | "running" | "backoff" | "failed" | "stopping";

export type LabsHealth = {
  state: LabsState;
  since: string;
  mode?: LabsMode;
  output?: string;
  width?: number;
  height?: number;
  target_fps?: number;
  fps: number;
  frames: number;
  dropped_frames: number;
  duplicate_frames: number;
  speed?: string;
  bitrate?: string;
  out_time_ms: number;
  audio_level_db?: number;
  stalled: boolean;
  restarts: number;
  last_error?: string;
  last_error_at?: string;
  next_retry_at?: string;
  started_at?: string;
  last_frame_at?: string;
  source?: { title?: string; item_kind?: string; item_id?: string; position_ms: number; playing: boolean };
};

export type LabsAcknowledgment = { user_id: string; at: string; notice_version: string };

export type LabsStatus = {
  notice: { version: string; text: string };
  acknowledgment: LabsAcknowledgment | null;
  config: LabsConfig;
  config_error?: string;
  capabilities: LabsCapabilities;
  health: LabsHealth;
  running: boolean;
  master_key: boolean;
};

export type LabsParty = {
  room_id: string;
  title: string;
  item_kind: string;
  item_id: string;
  members: number;
  playing: boolean;
};

export const discordLabsApi = {
  getDiscordInteractions: () => request<DiscordInteractionsStatus>("/api/v1/admin/integrations/discord/interactions"),
  registerDiscordCommands: (body: { guild_id?: string }) =>
    request<DiscordRegisterResult>("/api/v1/admin/integrations/discord/commands", { method: "POST", body }),
  setDiscordEndpoint: () =>
    request<{ ok: boolean; endpoint_url: string }>("/api/v1/admin/integrations/discord/endpoint", { method: "POST", body: {} }),
  deleteDiscordLink: (channelId: string) =>
    request(`/api/v1/admin/integrations/discord/links/${encodeURIComponent(channelId)}`, { method: "DELETE" }),

  getLabs: (refresh = false) => request<LabsStatus>(`/api/v1/admin/labs/vcam${refresh ? "?refresh=1" : ""}`),
  getLabsHealth: () => request<LabsHealth>("/api/v1/admin/labs/vcam/health"),
  listLabsParties: () => request<LabsParty[]>("/api/v1/admin/labs/vcam/parties"),
  acknowledgeLabs: (noticeVersion: string) =>
    request<LabsAcknowledgment>("/api/v1/admin/labs/vcam/acknowledge", {
      method: "POST",
      body: { notice_version: noticeVersion, accept: true },
    }),
  withdrawLabs: () => request("/api/v1/admin/labs/vcam/acknowledge", { method: "DELETE" }),
  putLabsConfig: (body: LabsConfigInput) =>
    request<{ config: LabsConfig; restart_required: boolean }>("/api/v1/admin/labs/vcam/config", { method: "PUT", body }),
  startLabs: () => request<LabsHealth>("/api/v1/admin/labs/vcam/start", { method: "POST", body: {} }),
  stopLabs: () => request<LabsHealth>("/api/v1/admin/labs/vcam/stop", { method: "POST", body: {} }),
  labsPreviewURL: (kind: "outgoing" | "raw", nonce: number) => `/api/v1/admin/labs/vcam/preview?kind=${kind}&t=${nonce}`,
};
