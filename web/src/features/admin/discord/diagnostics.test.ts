import { describe, expect, it } from "vitest";
import type { DiscordCheck } from "@/api/discordLabs";
import { groupChecks, modeLabel, needsAutoRun, overallLabel, worstStatus } from "./diagnostics";

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
    expect(groups[1].label).toBe("Bot connection");
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
