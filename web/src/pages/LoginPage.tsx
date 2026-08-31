import { FormEvent, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { Logo } from "@/components/brand/Logo";
import { api } from "@/api/api";
import { report } from "@/lib/journey";
import { useAuth } from "@/store/auth";

export function LoginPage() {
  const { me, system, login } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState(params.get("error") || "");

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
      setErr("Invalid credentials");
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg p-6 pt-[max(1.5rem,var(--sat))] pb-[max(1.5rem,var(--sab))]">
      <form onSubmit={onSubmit} className="w-full max-w-xs space-y-3 rounded-2xl border border-line bg-raised p-6">
        <Logo className="mx-auto h-24 w-24" />
        <h1 className="text-center text-lg font-semibold tracking-tight">View<span className="text-accent">Dock</span></h1>
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
        {err ? <p className="text-xs text-danger">{err}</p> : null}
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
    </div>
  );
}
