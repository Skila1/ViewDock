import { describe, expect, it } from "vitest";
import type { DiscordCheck } from "@/api/discordLabs";
import { diagnosticsRefetchMs, gatewaySummary, groupChecks, modeLabel, needsAutoRun, overallLabel, worstStatus } from "./diagnostics";

const check = (id: string, group: DiscordCheck["group"], status: DiscordCheck["status"]): DiscordCheck => ({
  id,
  group,
  label: id,
  status,
  detail: "",
  remote: false,
});

describe("discord diagnostics helpers", () => {
  it("ranks errors above warnings and ignores info and skipped", () => {
    expect(worstStatus([check("a", "bot", "ok"), check("b", "bot", "warn")])).toBe("warn");
    expect(worstStatus([check("a", "bot", "warn"), check("b", "bot", "error")])).toBe("error");
    expect(worstStatus([check("a", "bot", "info"), check("b", "bot", "skipped")])).toBe("info");
    expect(worstStatus([check("a", "bot", "skipped"), check("b", "bot", "ok")])).toBe("ok");
  });

  it("groups checks in display order and opens only groups with problems", () => {
    const groups = groupChecks([
      check("cmd", "commands", "ok"),
      check("mode", "configuration", "info"),
      check("bot", "bot", "error"),
    ]);
    expect(groups.map((g) => g.id)).toEqual(["configuration", "bot", "commands"]);
    expect(groups.map((g) => g.open)).toEqual([false, true, false]);
    expect(groups[1].label).toBe("Bot API (HTTP)");
  });

  it("keeps the Gateway separate from the HTTP checks", () => {
    const groups = groupChecks([
      check("endpoint", "interactions", "ok"),
      check("gw", "gateway", "warn"),
      check("bot", "bot", "ok"),
    ]);
    expect(groups.map((g) => g.id)).toEqual(["bot", "gateway", "interactions"]);
    expect(groups[1].label).toBe("Gateway (online presence)");
    expect(groups[2].label).toBe("HTTP interactions endpoint");
  });

  it("lists the Discord Activity checks last", () => {
    const groups = groupChecks([check("activity", "activity", "error"), check("mode", "configuration", "info")]);
    expect(groups.map((g) => g.id)).toEqual(["configuration", "activity"]);
    expect(groups[1]).toMatchObject({ label: "Discord Activity", open: true });
  });

  it("summarises the Gateway state", () => {
    const base = { presence: "offline", activity: "Watching movies & TV", token_rejected: false, attempts: 0 };
    expect(gatewaySummary({ ...base, state: "online", presence: "online" })).toEqual({ text: "Online, Watching movies & TV", status: "ok" });
    expect(gatewaySummary({ ...base, state: "reconnecting", attempts: 1 }).status).toBe("info");
    expect(gatewaySummary({ ...base, state: "reconnecting", attempts: 4 })).toEqual({ text: "Reconnecting (attempt 4)", status: "warn" });
    expect(gatewaySummary({ ...base, state: "failed", token_rejected: true })).toEqual({ text: "Offline, token rejected", status: "error" });
    expect(gatewaySummary({ ...base, state: "disabled" }).status).toBe("skipped");
  });

  it("refreshes faster while the Gateway is connecting", () => {
    const g = { presence: "offline", activity: "", token_rejected: false, attempts: 1 };
    expect(diagnosticsRefetchMs(undefined)).toBe(30_000);
    expect(diagnosticsRefetchMs({ gateway: { ...g, state: "reconnecting" } })).toBe(5_000);
    expect(diagnosticsRefetchMs({ gateway: { ...g, state: "online" } })).toBe(30_000);
  });

  it("describes the overall state and mode", () => {
    expect(overallLabel({ status: "error", checked_at: null })).toBe("Action required");
    expect(overallLabel({ status: "warn", checked_at: "2026-09-27T00:00:00Z" })).toBe("Needs attention");
    expect(overallLabel({ status: "ok", checked_at: null })).toBe("Configured");
    expect(overallLabel({ status: "ok", checked_at: "2026-09-27T00:00:00Z" })).toBe("Healthy");
    expect(modeLabel("shared")).toBe("Shared application");
    expect(modeLabel("separate")).toBe("Separate bot application");
  });

  it("runs automatically only when never checked or stale", () => {
    expect(needsAutoRun(undefined)).toBe(false);
    expect(needsAutoRun({ checked_at: null, stale: false })).toBe(true);
    expect(needsAutoRun({ checked_at: "2026-09-27T00:00:00Z", stale: true })).toBe(true);
    expect(needsAutoRun({ checked_at: "2026-09-27T00:00:00Z", stale: false })).toBe(false);
  });
});
