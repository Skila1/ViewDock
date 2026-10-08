import type { FlightEvent, SectionStatus } from "@/api/diagnostics";

export function statusTone(status: SectionStatus | string | undefined): string {
  switch (status) {
    case "ok":
      return "text-ok";
    case "degraded":
      return "text-warn";
    case "down":
      return "text-danger";
    default:
      return "text-dim";
  }
}

export function statusLabel(status: SectionStatus | string | undefined): string {
  switch (status) {
    case "ok":
      return "Healthy";
    case "degraded":
      return "Degraded";
    case "down":
      return "Offline";
    case "unconfigured":
      return "Not configured";
    default:
      return "Unknown";
  }
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "unknown";
  if (seconds < 60) return `${Math.round(seconds)} s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} h ${minutes % 60} min`;
  return `${Math.floor(hours / 24)} d ${hours % 24} h`;
}

export function formatAge(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "never";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "unknown";
  const diff = Math.max(0, (now - t) / 1000);
  if (diff < 5) return "just now";
  return `${formatDuration(diff)} ago`;
}

export function percent(rate: number): string {
  if (!Number.isFinite(rate)) return "n/a";
  return `${Math.round(rate * 100)}%`;
}

export function ms(value: number | undefined): string {
  if (!value || !Number.isFinite(value) || value <= 0) return "n/a";
  return value >= 10_000 ? `${(value / 1000).toFixed(1)} s` : `${Math.round(value)} ms`;
}

export function scoreTone(score: number, evidence: number, minEvidence: number): string {
  if (evidence < minEvidence) return "text-dim";
  if (score >= 80) return "text-ok";
  if (score >= 60) return "text-warn";
  return "text-danger";
}

const EVENT_LABELS: Record<string, string> = {
  session_created: "Session created",
  source_selected: "Source selected",
  first_frame: "First frame",
  startup_failed: "Startup failed",
  stall: "Stall",
  stall_end: "Stall ended",
  buffer: "Buffer level",
  bitrate_change: "Bitrate change",
  quality_change: "Quality change",
  failover: "Client failover",
  source_failover: "Source failover",
  pipeline_failed: "Pipeline failed",
  codec_error: "Codec error",
  error: "Player error",
  drift_correction: "Drift correction",
  reconnect: "Reconnect",
  recovered: "Recovered",
  seek: "Seek",
  node_unavailable: "Node unavailable",
};

export function eventLabel(type: string): string {
  if (EVENT_LABELS[type]) return EVENT_LABELS[type];
  if (type.startsWith("progress_")) return `Progress ${type.slice("progress_".length)}`;
  return type.replace(/[_.]/g, " ");
}

const PROBLEM_TYPES = new Set(["startup_failed", "pipeline_failed", "codec_error", "failover", "source_failover", "node_unavailable"]);

export function eventSeverity(e: FlightEvent): "problem" | "warning" | "info" {
  if (e.type === "error") return e.data?.fatal === true ? "problem" : "warning";
  if (e.type === "source_failover" && e.data?.ok === true) return "warning";
  if (PROBLEM_TYPES.has(e.type)) return "problem";
  if (e.type === "stall" || e.type === "reconnect" || e.type === "drift_correction") return "warning";
  return "info";
}

const HIDDEN_KEYS = new Set(["source", "device_class"]);

/** Renders event data as a short "key value" list, omitting server-filled keys. */
export function describeEventData(data: Record<string, unknown> | undefined, limit = 6): string {
  if (!data) return "";
  const parts: string[] = [];
  for (const key of Object.keys(data).sort()) {
    if (HIDDEN_KEYS.has(key)) continue;
    const value = data[key];
    if (value === null || value === undefined || typeof value === "object") continue;
    let text = String(value);
    if (typeof value === "number" && key.endsWith("_ms")) text = ms(value) === "n/a" ? "0 ms" : ms(value);
    parts.push(`${key.replace(/_ms$/, "").replace(/_/g, " ")} ${text}`);
    if (parts.length >= limit) break;
  }
  return parts.join(", ");
}

export function parseCandidates(input: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of input.split(/[\s,]+/)) {
    const v = raw.trim();
    if (v && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}
