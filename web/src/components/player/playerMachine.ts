export type PlayerPhase =
  | "idle"
  | "creatingSession"
  | "attaching"
  | "playing"
  | "paused"
  | "switchingQuality"
  | "recreating"
  | "ended"
  | "destroyed";

export type PlayerEvent =
  | "START"
  | "SESSION_CREATED"
  | "ATTACHED"
  | "PLAY"
  | "PAUSE"
  | "QUALITY"
  | "GONE"
  | "ENDED"
  | "DESTROY"
  | "ERROR";

const TABLE: Record<PlayerPhase, Partial<Record<PlayerEvent, PlayerPhase>>> = {
  idle: { START: "creatingSession", DESTROY: "destroyed" },
  creatingSession: {
    SESSION_CREATED: "attaching",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  attaching: {
    ATTACHED: "paused",
    PLAY: "playing",
    PAUSE: "paused",
    GONE: "recreating",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  playing: {
    PAUSE: "paused",
    QUALITY: "switchingQuality",
    GONE: "recreating",
    ENDED: "ended",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  paused: {
    PLAY: "playing",
    QUALITY: "switchingQuality",
    GONE: "recreating",
    ENDED: "ended",
    START: "creatingSession",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  switchingQuality: {
    SESSION_CREATED: "attaching",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  recreating: {
    SESSION_CREATED: "attaching",
    DESTROY: "destroyed",
    ERROR: "idle",
  },
  ended: { START: "creatingSession", DESTROY: "destroyed" },
  destroyed: {},
};

export function reducePlayer(phase: PlayerPhase, event: PlayerEvent): PlayerPhase {
  return TABLE[phase][event] ?? phase;
}

export function isActivePhase(phase: PlayerPhase): boolean {
  return phase !== "idle" && phase !== "ended" && phase !== "destroyed";
}
