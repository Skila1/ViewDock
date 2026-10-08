import type { DiscordSDK } from "@discord/embedded-app-sdk";

// Sent on every request from inside the Discord Activity so the server issues
// cookies that work in Discord's third-party iframe. It changes cookie
// attributes only; it grants nothing.
export const EMBED_HEADER = "X-ViewDock-Embed";
const EMBED_VALUE = "discord-activity";
const LAUNCH_KEY = "vd.activity.launch";

function hasSdkParams(params: URLSearchParams | null): boolean {
  return Boolean(params?.get("frame_id") && params?.get("instance_id"));
}

function readStored(): string | null {
  try {
    return window.sessionStorage.getItem(LAUNCH_KEY);
  } catch {
    return null;
  }
}

function store(search: string) {
  try {
    window.sessionStorage.setItem(LAUNCH_KEY, search);
  } catch {
    // Storage can be blocked in third-party frames; a reload then needs a relaunch.
  }
}

// Discord only adds the launch parameters to the first page load. They are
// kept for the tab so a reload after in-app navigation can still connect.
const launchSearch = (() => {
  if (typeof window === "undefined") return "";
  const current = new URLSearchParams(window.location.search);
  if (hasSdkParams(current)) {
    store(window.location.search);
    return window.location.search;
  }
  if (!window.location.hostname.endsWith(".discordsays.com")) return "";
  const stored = readStored();
  return stored && hasSdkParams(new URLSearchParams(stored)) ? stored : "";
})();

const launch = launchSearch ? new URLSearchParams(launchSearch) : null;

const activity =
  typeof window !== "undefined" && (window.location.hostname.endsWith(".discordsays.com") || hasSdkParams(launch));

export function isDiscordActivity(): boolean {
  return activity;
}

export function embedHeaders(): Record<string, string> {
  return activity ? { [EMBED_HEADER]: EMBED_VALUE } : {};
}

// launchedAtRoot reports whether Discord opened the app's start page, as
// opposed to a reload of a page the user navigated to.
export const launchedAtRoot = typeof window !== "undefined" && window.location.pathname === "/";

let sdk: DiscordSDK | null = null;
let connecting: Promise<DiscordSDK> | null = null;

export function activitySdk(): DiscordSDK | null {
  return sdk;
}

export function activityInstanceId(): string {
  return sdk?.instanceId ?? launch?.get("instance_id") ?? "";
}

// connectActivity performs the Discord handshake. The SDK reads the launch
// parameters from the address bar, so they are restored for the moment it is
// constructed when in-app navigation has removed them.
export function connectActivity(clientId: string): Promise<DiscordSDK> {
  if (sdk) return Promise.resolve(sdk);
  if (!connecting) {
    connecting = (async () => {
      if (!launch) throw new Error("Discord did not pass the Activity launch details. Close the Activity and start it again.");
      const { DiscordSDK } = await import("@discord/embedded-app-sdk");
      const { pathname, search, hash } = window.location;
      const restore = !hasSdkParams(new URLSearchParams(search));
      if (restore) window.history.replaceState(window.history.state, "", pathname + launchSearch + hash);
      let next: DiscordSDK;
      try {
        next = new DiscordSDK(clientId);
      } finally {
        if (restore) window.history.replaceState(window.history.state, "", pathname + search + hash);
      }
      await next.ready();
      sdk = next;
      return next;
    })().finally(() => {
      connecting = null;
    });
  }
  return connecting;
}

const LEFT_KEY = "vd.activity.left";

// The watch party this viewer chose to leave in this Activity session. While
// it is still running they are not sent back into it; they watch on their
// own and can rejoin from the Activity's start page.
export function leftPartyCode(): string {
  try {
    const raw = window.sessionStorage.getItem(LEFT_KEY);
    if (!raw) return "";
    const v = JSON.parse(raw) as { instance?: string; code?: string };
    return v.instance === activityInstanceId() ? (v.code ?? "") : "";
  } catch {
    return "";
  }
}

export function rememberLeftParty(code: string) {
  try {
    window.sessionStorage.setItem(LEFT_KEY, JSON.stringify({ instance: activityInstanceId(), code }));
  } catch {
    // Without storage the choice lasts until the page reloads (router state).
  }
}

export function forgetLeftParty() {
  try {
    window.sessionStorage.removeItem(LEFT_KEY);
  } catch {
    /* nothing stored */
  }
}
