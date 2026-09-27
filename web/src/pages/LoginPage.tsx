import { FormEvent, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { Logo } from "@/components/brand/Logo";
import { api } from "@/api/api";
import { report } from "@/lib/journey";
import { useAuth } from "@/store/auth";

function loginError(raw: string) {
  switch (raw) {
    case "not_in_server":
      return "Your Discord account is not in the allowed server.";
    case "missing_role":
      return "Your Discord account does not have the required role.";
    case "disabled":
      return "This account is disabled.";
    case "oauth_denied":
      return "Discord sign-in was cancelled.";
    case "discord registration is disabled":
      return "Discord registration is off. Ask an admin to enable it or link your account.";
    case "local_login_disabled":
      return "Local sign-in is off. Use Discord.";
    default:
      return raw || "Sign-in failed";
  }
}

export function LoginPage() {
  const { me, system, login } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState(loginError(params.get("error") || ""));
  const discordOnly = Boolean(system?.discord_configured || system?.local_login_disabled);

  if (system?.setup_needed) return <Navigate to="/setup" replace />;
  if (me) return <Navigate to="/" replace />;

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    report("login_attempt", { username });
    try {
      await login(username, password);
      report("login_ok", { username });
      await api.ensureCsrf();
      navigate("/");
    } catch {
      report("login_fail", { username });
      setErr(discordOnly ? "Local sign-in is off. Use Discord." : "Invalid credentials");
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg p-6 pt-[max(1.5rem,var(--sat))] pb-[max(1.5rem,var(--sab))]">
      <div className="w-full max-w-xs space-y-3 rounded-2xl border border-line bg-raised p-6">
        <Logo className="mx-auto h-24 w-24" />
        <h1 className="text-center text-lg font-semibold tracking-tight">View<span className="text-accent">Dock</span></h1>
        {err ? <p className="text-xs text-danger">{err}</p> : null}

        {discordOnly ? (
          <>
            <a
              href="/api/v1/auth/discord"
              className="btn-green tap flex w-full items-center justify-center rounded-full text-sm"
            >
              Continue with Discord
            </a>
            <p className="text-center text-xs text-dim">Local username sign-in is off.</p>
          </>
        ) : (
          <form onSubmit={onSubmit} className="space-y-3">
            <input
              autoComplete="username"
              placeholder="Username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full"
            />
            <input
              type="password"
              autoComplete="current-password"
              placeholder="Password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full"
            />
            <button type="submit" className="btn-green tap w-full rounded-full text-sm">
              Sign in
            </button>
            {system?.discord_configured ? (
              <a
                href="/api/v1/auth/discord"
                className="tap flex w-full items-center justify-center rounded-full border border-line text-sm"
              >
                Continue with Discord
              </a>
            ) : null}
          </form>
        )}
      </div>
    </div>
  );
}
