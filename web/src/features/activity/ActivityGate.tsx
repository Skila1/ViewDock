import { useEffect, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import type { DiscordSDK } from "@discord/embedded-app-sdk";
import { api, ApiError } from "@/api/api";
import { Logo } from "@/components/brand/Logo";
import { connectActivity, launchedAtRoot } from "@/lib/discordActivity";
import { report } from "@/lib/journey";
import { loginError } from "@/pages/LoginPage";

type Scopes = Parameters<DiscordSDK["commands"]["authorize"]>[0]["scope"];

type Phase = { state: "connecting" } | { state: "ready" } | { state: "error"; message: string };

let started: Promise<void> | null = null;

function activityError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "denied") return loginError(err.message);
    if (err.code === "disabled") return "The ViewDock Activity is off. An administrator can turn it on under Admin, Discord.";
    return err.message;
  }
  if (err instanceof Error && err.message) return err.message;
  if (err && typeof err === "object" && "message" in err && typeof err.message === "string") return err.message;
  return "Could not connect to Discord.";
}

async function signedIn(): Promise<boolean> {
  try {
    await api.getMe();
    return true;
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return false;
    if (err instanceof ApiError && err.status === 423) return true;
    throw err;
  }
}

// startActivity connects to the Discord client and, when needed, signs in
// with the Discord account running the Activity. It runs once per page load.
function startActivity(): Promise<void> {
  if (!started) {
    started = (async () => {
      await api.ensureCsrf().catch(() => null);
      const cfg = await api.getActivityConfig();
      if (!cfg.enabled || !cfg.client_id) {
        throw new ApiError(503, "The ViewDock Activity is off. An administrator can turn it on under Admin, Discord.", "disabled");
      }
      const sdk = await connectActivity(cfg.client_id);
      if (await signedIn()) return;
      const { code } = await sdk.commands.authorize({
        client_id: cfg.client_id,
        response_type: "code",
        state: "",
        prompt: "none",
        scope: (cfg.scopes?.length ? cfg.scopes : ["identify"]) as Scopes,
      });
      const out = await api.activitySignIn(code);
      report("activity_sign_in");
      await sdk.commands.authenticate({ access_token: out.access_token }).catch((err: unknown) => {
        console.warn("Discord Activity authenticate failed", err);
      });
    })().catch((err: unknown) => {
      started = null;
      throw err;
    });
  }
  return started;
}

export function ActivityGate({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const [phase, setPhase] = useState<Phase>({ state: "connecting" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    setPhase({ state: "connecting" });
    startActivity()
      .then(() => {
        if (!live) return;
        if (launchedAtRoot && window.location.pathname === "/") navigate("/activity", { replace: true });
        setPhase({ state: "ready" });
      })
      .catch((err: unknown) => {
        report("activity_fail", { message: activityError(err) });
        if (live) setPhase({ state: "error", message: activityError(err) });
      });
    return () => {
      live = false;
    };
  }, [attempt, navigate]);

  if (phase.state === "ready") return children;
  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg p-6">
      <div className="w-full max-w-xs space-y-3 rounded-2xl border border-line bg-raised p-6 text-center">
        <Logo className="mx-auto h-16 w-16" />
        {phase.state === "connecting" ? (
          <p className="text-sm text-dim">Connecting to Discord…</p>
        ) : (
          <>
            <p className="text-sm text-danger">{phase.message}</p>
            <button
              type="button"
              className="tap w-full rounded-full border border-line text-sm"
              onClick={() => setAttempt((n) => n + 1)}
            >
              Try again
            </button>
          </>
        )}
      </div>
    </div>
  );
}
