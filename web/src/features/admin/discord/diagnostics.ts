import type {
  DiscordCheck,
  DiscordCheckGroup,
  DiscordCheckStatus,
  DiscordDiagnostics,
  DiscordGatewayStatus,
} from "@/api/discordLabs";

export const GROUP_LABELS: Record<DiscordCheckGroup, string> = {
  configuration: "Configuration",
  oauth: "Sign-in (OAuth)",
  bot: "Bot API (HTTP)",
  gateway: "Gateway (online presence)",
  servers: "Servers and permissions",
  commands: "Slash commands",
  interactions: "HTTP interactions endpoint",
};

const GROUP_ORDER: DiscordCheckGroup[] = ["configuration", "oauth", "bot", "gateway", "servers", "commands", "interactions"];

/** Short Gateway summary with the tone of its state. */
export function gatewaySummary(g: DiscordGatewayStatus): { text: string; status: DiscordCheckStatus } {
  switch (g.state) {
    case "online":
      return { text: `Online, ${g.activity}`, status: "ok" };
    case "connecting":
      return { text: "Connecting", status: "info" };
    case "reconnecting":
      return { text: `Reconnecting (attempt ${g.attempts})`, status: g.attempts > 1 ? "warn" : "info" };
    case "failed":
      return { text: g.token_rejected ? "Offline, token rejected" : "Offline, connection failed", status: "error" };
    case "disabled":
      return { text: "Offline, no bot token", status: "skipped" };
    default:
      return { text: "Offline", status: "skipped" };
  }
}

/** While the Gateway is changing state, diagnostics refresh faster so the panel follows it. */
export function diagnosticsRefetchMs(d: Pick<DiscordDiagnostics, "gateway"> | undefined): number {
  const s = d?.gateway?.state;
  return s === "connecting" || s === "reconnecting" ? 5_000 : 30_000;
}

const RANK: Record<DiscordCheckStatus, number> = { error: 3, warn: 2, ok: 1, info: 0, skipped: 0 };

/** The most severe status among checks; info and skipped count as neutral. */
export function worstStatus(checks: Pick<DiscordCheck, "status">[]): DiscordCheckStatus {
  let worst: DiscordCheckStatus = "info";
  for (const c of checks) {
    if (RANK[c.status] > RANK[worst]) worst = c.status;
  }
  return worst;
}

export type CheckGroup = {
  id: DiscordCheckGroup;
  label: string;
  status: DiscordCheckStatus;
  checks: DiscordCheck[];
  /** Groups with a warning or error start expanded. */
  open: boolean;
};

export function groupChecks(checks: DiscordCheck[]): CheckGroup[] {
  return GROUP_ORDER.map((id) => {
    const items = checks.filter((c) => c.group === id);
    const status = worstStatus(items);
    return { id, label: GROUP_LABELS[id], status, checks: items, open: status === "warn" || status === "error" };
  }).filter((g) => g.checks.length > 0);
}

export function statusTone(status: DiscordCheckStatus | DiscordDiagnostics["status"]): string {
  switch (status) {
    case "ok":
      return "text-ok";
    case "warn":
      return "text-warn";
    case "error":
      return "text-danger";
    default:
      return "text-dim";
  }
}

export function overallLabel(d: Pick<DiscordDiagnostics, "status" | "checked_at">): string {
  switch (d.status) {
    case "error":
      return "Action required";
    case "warn":
      return "Needs attention";
    default:
      return d.checked_at ? "Healthy" : "Configured";
  }
}

export function modeLabel(mode: DiscordDiagnostics["mode"]): string {
  return mode === "separate" ? "Separate bot application" : "Shared application";
}

/** Diagnostics run automatically when Discord was never checked or the configuration changed since. */
export function needsAutoRun(d: Pick<DiscordDiagnostics, "checked_at" | "stale"> | undefined): boolean {
  return Boolean(d && (!d.checked_at || d.stale));
}
