import { create } from "zustand";
import { api, ApiError } from "@/api/api";
import type { Me, SystemInfo } from "@/types/api.gen";

export type GuestCaps = {
  shareToken: string;
  itemKind: string;
  itemId: string;
  title?: string;
  canDownload: boolean;
  canWatchTogether: boolean;
};

type AuthState = {
  ready: boolean;
  system: SystemInfo | null;
  me: Me | null;
  guest: GuestCaps | null;
  pinLocked: boolean;
  error: string | null;
  boot: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  unlockPin: (pin: string) => Promise<void>;
  setGuest: (guest: GuestCaps | null) => void;
};

export const useAuth = create<AuthState>((set) => ({
  ready: false,
  system: null,
  me: null,
  guest: null,
  pinLocked: false,
  error: null,

  boot: async () => {
    try {
      await api.ensureCsrf();
      const system = await api.getSystem();
      let me: Me | null = null;
      let pinLocked = false;
      if (!system.setup_needed) {
        try {
          me = await api.getMe();
          pinLocked = Boolean(me.pin_locked);
        } catch (err) {
          if (err instanceof ApiError && err.status === 423) {
            pinLocked = true;
          } else {
            me = null;
          }
        }
      }
      set({ system, me, pinLocked, ready: true, error: null });
    } catch (err) {
      set({
        ready: true,
        error: err instanceof Error ? err.message : "boot failed",
      });
    }
  },

  login: async (username, password) => {
    const me = await api.login({ username, password });
    set({ me, pinLocked: Boolean(me.pin_locked), error: null });
  },

  logout: async () => {
    await api.logout();
    set({ me: null, pinLocked: false, guest: null });
  },

  unlockPin: async (pin) => {
    await api.unlockPin(pin);
    const me = await api.getMe();
    set({ me, pinLocked: false });
  },

  setGuest: (guest) => set({ guest }),
}));
