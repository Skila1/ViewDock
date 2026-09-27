import { afterEach, describe, expect, it, vi } from "vitest";

async function load(url: string) {
  window.history.replaceState(null, "", url);
  vi.resetModules();
  return import("./discordActivity");
}

describe("discord activity detection", () => {
  afterEach(() => {
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/");
  });

  it("stays off in a normal browser tab", async () => {
    const mod = await load("/movies?q=x");
    expect(mod.isDiscordActivity()).toBe(false);
    expect(mod.embedHeaders()).toEqual({});
    expect(mod.activityInstanceId()).toBe("");
  });

  it("recognises a Discord launch and marks requests as embedded", async () => {
    const mod = await load("/?frame_id=f1&instance_id=i-abc&platform=desktop");
    expect(mod.isDiscordActivity()).toBe(true);
    expect(mod.launchedAtRoot).toBe(true);
    expect(mod.embedHeaders()).toEqual({ [mod.EMBED_HEADER]: "discord-activity" });
    expect(mod.activityInstanceId()).toBe("i-abc");
  });

  it("needs both launch parameters", async () => {
    const mod = await load("/?frame_id=f1");
    expect(mod.isDiscordActivity()).toBe(false);
  });

  it("does not reuse saved launch details outside Discord", async () => {
    window.sessionStorage.setItem("vd.activity.launch", "?frame_id=f1&instance_id=i-abc");
    const mod = await load("/together/abc");
    expect(mod.isDiscordActivity()).toBe(false);
    expect(mod.activityInstanceId()).toBe("");
  });
});
