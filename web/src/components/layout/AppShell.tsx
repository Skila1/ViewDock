import { FormEvent, useState } from "react";
import { Link, NavLink, Outlet, useNavigate } from "react-router";
import { Clapperboard, Home, LogOut, Search, Settings, Shield, Tv } from "lucide-react";
import { Logo } from "@/components/brand/Logo";
import { useAuth } from "@/store/auth";
import { cn } from "@/lib/cn";

const nav = [
  { to: "/", label: "Home", icon: Home, end: true },
  { to: "/search", label: "Search", icon: Search },
  { to: "/movies", label: "Movies", icon: Clapperboard },
  { to: "/tv", label: "TV", icon: Tv },
];

export function AppShell() {
  const { me, logout, pinLocked, unlockPin } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [pin, setPin] = useState("");
  const [pinErr, setPinErr] = useState("");

  const onSearch = (e: FormEvent) => {
    e.preventDefault();
    if (q.trim()) navigate(`/search?q=${encodeURIComponent(q.trim())}`);
  };

  if (pinLocked) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg p-6">
        <form
          className="w-full max-w-xs space-y-3 rounded-2xl border border-line bg-raised p-6"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              await unlockPin(pin);
            } catch {
              setPinErr("Invalid PIN");
            }
          }}
        >
          <Logo className="h-10 w-10" />
          <h1 className="text-base font-semibold">Unlock</h1>
          <input
            type="password"
            inputMode="numeric"
            autoComplete="off"
            placeholder="PIN"
            value={pin}
            onChange={(e) => setPin(e.target.value)}
          />
          {pinErr ? <p className="text-xs text-danger">{pinErr}</p> : null}
          <button type="submit" className="btn-green w-full rounded-full px-3 py-2 text-sm">
            Continue
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh bg-bg">
      <aside className="hidden w-[232px] shrink-0 flex-col border-r border-line bg-raised/80 md:flex">
        <Link to="/" className="flex items-center gap-2 bg-black px-3 py-4">
          <Logo className="h-10 w-10" />
          <span className="text-sm font-bold tracking-wide">ViewDock</span>
        </Link>
        <nav className="flex-1 space-y-0.5 px-2 pt-3">
          {nav.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink",
                  isActive && "bg-overlay text-ink",
                )
              }
            >
              <it.icon className="h-4 w-4 shrink-0" />
              {it.label}
            </NavLink>
          ))}
          {me?.is_admin ? (
            <NavLink
              to="/admin"
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink",
                  isActive && "bg-overlay text-ink",
                )
              }
            >
              <Shield className="h-4 w-4 shrink-0" />
              Admin
            </NavLink>
          ) : null}
        </nav>
        <div className="border-t border-line p-2">
          <NavLink
            to="/profile"
            className={({ isActive }) =>
              cn(
                "flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink",
                isActive && "bg-overlay text-ink",
              )
            }
          >
            <Settings className="h-4 w-4 shrink-0" />
            <span className="truncate">{me?.display_name || me?.username || "Profile"}</span>
          </NavLink>
          <button
            type="button"
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink"
            onClick={async () => {
              await logout();
              navigate("/login");
            }}
          >
            <LogOut className="h-4 w-4" />
            Log out
          </button>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-[var(--nav-h)] items-center gap-3 border-b border-line bg-bg/80 px-4 backdrop-blur">
          <Link to="/" className="flex items-center gap-2 md:hidden">
            <Logo className="h-8 w-8" />
          </Link>
          <form onSubmit={onSearch} className="flex min-w-0 flex-1 items-center gap-2">
            <Search size={16} className="shrink-0 text-dim" />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search movies and TV"
              className="h-9 w-full max-w-md border-0 bg-transparent px-0"
            />
          </form>
        </header>
        <main className="px-4 py-5 md:px-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
