import { request } from "./client";
import { sessionPath } from "./api";

export type SectionStatus = "ok" | "degraded" | "down" | "unconfigured";

export type Section<T> = {
  status: SectionStatus;
  error?: string;
  checked_at: string;
  stale?: boolean;
  data?: T;
};

export type FlightEvent = {
  at: string;
  session_id: string;
  type: string;
  origin?: "server" | "client";
  request_id?: string;
  correlation_id?: string;
  data?: Record<string, unknown>;
  /** Media worker that recorded the event, when it was not this server. */
  node?: string;
};

export type FlightSessionSummary = {
  session_id: string;
  first_at: string;
  last_at: string;
  events: number;
  last_type: string;
  notable: number;
  node?: string;
};

export type FlightStats = {
  sessions: number;
  max_sessions: number;
  max_events: number;
  retention_seconds: number;
  recorded: number;
  dropped: number;
  sampled: number;
  budget_burst: number;
  budget_per_second: number;
};

export type ControlData = { version: string; role: string; uptime_seconds: number };

export type DatabaseData = {
  driver: string;
  available: boolean;
  watched: boolean;
  trips: number;
  since?: string;
  pool: { max_open: number; open: number; in_use: number; idle: number; wait_count: number; wait_duration_ms: number };
};

export type StorageData = { provider?: string; latency_ms?: number };

export type CoordinatorData = {
  available: boolean;
  rooms: number;
  participants: number;
  details?: Record<string, unknown>;
};

export type MeshNode = {
  id: string;
  name: string;
  endpoint: string;
  role: string;
  region?: string;
  status: string;
  enabled: boolean;
  draining: boolean;
  priority: number;
  weight: number;
  capacity: number;
  latency_ms: number;
  failures: number;
  last_failure_at?: string;
  last_error?: string;
  updated_at: string;
  health?: Record<string, unknown>;
};

export type NodesData = {
  total: number;
  healthy: number;
  unhealthy: number;
  draining: number;
  disabled: number;
  items: MeshNode[];
};

export type ProgressData = { reports: number; writes: number; writes_per_report: number };

export type ScopeScore = {
  scope: string;
  region?: string;
  device_class?: string;
  score: number;
  success_rate: number;
  stalls_per_session: number;
  latency_ms: number;
  ttff_ms: number;
  evidence: number;
};

export type ReliabilityFailure = {
  at: string;
  kind: string;
  reason?: string;
  region?: string;
  device_class?: string;
  session_id?: string;
  request_id?: string;
};

export type OverrideMode = "prefer" | "avoid" | "exclude";

export type ReliabilityOverride = {
  source: string;
  mode: OverrideMode;
  note?: string;
  actor_id?: string;
  updated_at: string;
};

export type SourceReport = {
  source: string;
  score: ScopeScore;
  scopes: ScopeScore[];
  recent_failures: ReliabilityFailure[];
  last_seen?: string;
  last_failure?: string;
  override?: ReliabilityOverride;
};

export type ReliabilityData = {
  half_life_seconds: number;
  min_evidence: number;
  sources: SourceReport[];
  overrides: ReliabilityOverride[];
  truncated?: boolean;
};

export type RankedSource = {
  source: string;
  rank: number;
  excluded: boolean;
  override?: OverrideMode;
  metrics: ScopeScore;
  explanation: string;
};

export type RankDecision = {
  selected: string;
  region?: string;
  device_class?: string;
  ranking: RankedSource[];
  explanation: string;
};

export type ResilienceDashboard = {
  generated_at: string;
  control: Section<ControlData>;
  database: Section<DatabaseData>;
  storage: Section<StorageData>;
  coordinator: Section<CoordinatorData>;
  nodes: Section<NodesData>;
  progress: Section<ProgressData>;
  flight_recorder: Section<{ stats: FlightStats; sessions: FlightSessionSummary[] }>;
  failovers: Section<{ events: FlightEvent[] }>;
  reliability: Section<ReliabilityData>;
  extra?: Record<string, Section<unknown>>;
};

export type TelemetryEvent = { type: string; at: number; data?: Record<string, unknown> };
export type TelemetryResult = { accepted: number; rejected: number; dropped: number };

function newRequestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

export const diagnosticsApi = {
  resilience: () => request<ResilienceDashboard>("/api/v1/admin/resilience"),
  flightSessions: async () =>
    (await request<{ items: FlightSessionSummary[] | null }>("/api/v1/admin/resilience/sessions")).items ?? [],
  flightTimeline: async (sessionId: string) =>
    (await request<FlightEvent[] | null>(`/api/v1/admin/diagnostics/flight-recorder/${encodeURIComponent(sessionId)}`)) ?? [],
  reliability: () => request<ReliabilityData>("/api/v1/admin/resilience/reliability"),
  rank: (candidates: string[], region = "", deviceClass = "") => {
    const q = new URLSearchParams({ candidates: candidates.join(",") });
    if (region) q.set("region", region);
    if (deviceClass) q.set("device_class", deviceClass);
    return request<RankDecision>(`/api/v1/admin/resilience/reliability/rank?${q.toString()}`);
  },
  setOverride: (body: { source: string; mode: OverrideMode; note?: string }) =>
    request<{ override: ReliabilityOverride; persisted: boolean }>("/api/v1/admin/resilience/reliability/overrides", {
      method: "PUT",
      body,
    }),
  clearOverride: (source: string) =>
    request<null>(`/api/v1/admin/resilience/reliability/overrides?source=${encodeURIComponent(source)}`, { method: "DELETE" }),
  /**
   * Sends a batch of player telemetry to the session's flight recorder. Each
   * batch carries its own X-Request-Id; correlationId ties batches from one
   * playback attempt together across the frontend, controller and workers.
   */
  sendSessionTelemetry: (sessionId: string, events: TelemetryEvent[], correlationId?: string) =>
    request<TelemetryResult>(sessionPath(sessionId, "/telemetry"), {
      method: "POST",
      headers: { "X-Request-Id": newRequestId() },
      body: correlationId ? { events, correlation_id: correlationId } : { events },
    }),
};
