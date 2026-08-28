import { FormEvent, useState } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { Logo } from "@/components/brand/Logo";
import { api } from "@/api/api";
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
    try {
      await login(username, password);
      await api.ensureCsrf();
      navigate("/");
    } catch {
      setErr("Invalid credentials");
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg p-6">
      <form onSubmit={onSubmit} className="w-full max-w-xs space-y-3 rounded-2xl border border-line bg-raised p-6">
        <Logo className="h-12 w-12" />
        <h1 className="text-lg font-semibold tracking-tight">ViewDock</h1>
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
        <button type="submit" className="btn-green w-full rounded-full py-2 text-sm">
          Sign in
        </button>
        {system?.discord_configured ? (
          <a
            href="/api/v1/auth/discord"
            className="block w-full rounded-full border border-line py-2 text-center text-sm"
          >
            Continue with Discord
          </a>
        ) : null}
      </form>
    </div>
  );
}
