import { create } from "zustand";
import { api, ApiError } from "@/api/api";
import { report, setJourneyContext } from "@/lib/journey";
import { clearSnapshots, setSnapshotScope } from "@/lib/snapshotCache";
import { signOutVault } from "@/lib/vault/manager";
import { clearStreamCaches } from "@/playback/streamCache";
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
  // offline is set when boot could not reach the server and had no saved state.
  offline: boolean;
  boot: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  unlockPin: (pin: string) => Promise<void>;
  setGuest: (guest: GuestCaps | null) => void;
};

function unreachable(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 0 || err.status === 502 || err.status === 503 || err.status === 504);
}

export const useAuth = create<AuthState>((set, get) => ({
  ready: false,
  system: null,
  me: null,
  guest: null,
  pinLocked: false,
  error: null,
  offline: false,

  boot: async () => {
    try {
      await api.ensureCsrf().catch(() => null);
      const system = await api.getSystem();
      let me: Me | null = null;
      let pinLocked = false;
      if (!system.setup_needed) {
        try {
          me = await api.getMe();
          pinLocked = Boolean(me.pin_locked);
          setSnapshotScope(me.id);
        } catch (err) {
          if (err instanceof ApiError && err.status === 423) {
            pinLocked = true;
          } else {
            me = null;
          }
        }
      }
      setJourneyContext({ user_id: me?.id, username: me?.username });
      report("boot", { setup_needed: system.setup_needed, signed_in: Boolean(me), pin_locked: pinLocked });
      set({ system, me, pinLocked, ready: true, error: null, offline: false });
    } catch (err) {
      report("boot_fail", { message: err instanceof Error ? err.message : "boot failed" });
      set({
        ready: true,
        offline: unreachable(err),
        error: err instanceof Error ? err.message : "boot failed",
      });
    }
  },

  login: async (username, password) => {
    const me = await api.login({ username, password });
    setSnapshotScope(me.id);
    setJourneyContext({ user_id: me.id, username: me.username });
    set({ me, pinLocked: Boolean(me.pin_locked), error: null });
  },

  logout: async () => {
    report("logout");
    const userId = get().me?.id;
    try {
      await api.logout();
    } finally {
      await signOutVault(userId).catch(() => {});
      await clearSnapshots();
      await clearStreamCaches().catch(() => {});
      setSnapshotScope(null);
      navigator.serviceWorker?.controller?.postMessage({ type: "viewdock:clear-user-caches" });
      setJourneyContext({ user_id: undefined, username: undefined });
      set({ me: null, pinLocked: false, guest: null });
    }
  },

  unlockPin: async (pin) => {
    await api.unlockPin(pin);
    const me = await api.getMe();
    set({ me, pinLocked: false });
  },

  setGuest: (guest) => set({ guest }),
}));
