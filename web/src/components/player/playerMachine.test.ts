import { describe, expect, it } from "vitest";
import { reducePlayer, type PlayerEvent, type PlayerPhase } from "./playerMachine";

function run(start: PlayerPhase, events: PlayerEvent[]): PlayerPhase {
  return events.reduce((phase, ev) => reducePlayer(phase, ev), start);
}

describe("player machine", () => {
  it("starts a session and plays", () => {
    expect(run("idle", ["START", "SESSION_CREATED", "ATTACHED", "PLAY"])).toBe("playing");
  });

  it("pauses and resumes", () => {
    expect(run("playing", ["PAUSE", "PLAY"])).toBe("playing");
    expect(run("playing", ["PAUSE"])).toBe("paused");
  });

  it("quality change recreates via attaching", () => {
    expect(run("playing", ["QUALITY", "SESSION_CREATED", "ATTACHED", "PLAY"])).toBe("playing");
    expect(reducePlayer("playing", "QUALITY")).toBe("switchingQuality");
  });

  it("410 gone recreates at the same item", () => {
    expect(reducePlayer("playing", "GONE")).toBe("recreating");
    expect(run("playing", ["GONE", "SESSION_CREATED", "PLAY"])).toBe("playing");
  });

  it("ends and can restart", () => {
    expect(run("playing", ["ENDED"])).toBe("ended");
    expect(run("ended", ["START"])).toBe("creatingSession");
  });

  it("destroy is terminal", () => {
    expect(run("playing", ["DESTROY", "START"])).toBe("destroyed");
  });
});
