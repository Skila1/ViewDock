import { create } from "zustand";
import type { PlaybackSession } from "@/types/api.gen";
import type { PlayerPhase } from "@/components/player/playerMachine";

type PlayerStore = {
  phase: PlayerPhase;
  session: PlaybackSession | null;
  quality: string | null;
  resumeMs: number;
  setPhase: (phase: PlayerPhase) => void;
  setSession: (session: PlaybackSession | null) => void;
  setQuality: (quality: string | null) => void;
  setResumeMs: (ms: number) => void;
  reset: () => void;
};

export const usePlayerStore = create<PlayerStore>((set) => ({
  phase: "idle",
  session: null,
  quality: null,
  resumeMs: 0,
  setPhase: (phase) => set({ phase }),
  setSession: (session) => set({ session }),
  setQuality: (quality) => set({ quality }),
  setResumeMs: (resumeMs) => set({ resumeMs }),
  reset: () => set({ phase: "idle", session: null, quality: null, resumeMs: 0 }),
}));
