import { FormEvent, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { ArrowLeft, Clapperboard, Download, Home, LogOut, Search, Settings, Shield, Tv } from "lucide-react";
import { Logo } from "@/components/brand/Logo";
import { useAuth } from "@/store/auth";
import { cn } from "@/lib/cn";
import { ConnectivityBanner } from "@/components/layout/ConnectivityBanner";
import { ADMIN_SECTIONS, isAdminPath } from "@/features/admin/adminNav";
import { SubnavSidebar, useAdminSubnav } from "@/features/admin/AdminSubnav";

const sideLink = "flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink";

const nav = [
  { to: "/", label: "Home", icon: Home, end: true },
  { to: "/search", label: "Search", icon: Search },
  { to: "/movies", label: "Movies", icon: Clapperboard },
  { to: "/tv", label: "TV", icon: Tv },
  { to: "/offline", label: "Offline", icon: Download },
];

export function AppShell() {
  const { me, logout, pinLocked, unlockPin } = useAuth();
  const navigate = useNavigate();
  const inAdmin = isAdminPath(useLocation().pathname);
  const subnav = useAdminSubnav(Boolean(me?.is_admin && inAdmin));
  const [q, setQ] = useState("");
  const [pin, setPin] = useState("");
  const [pinErr, setPinErr] = useState("");

  const onSearch = (e: FormEvent) => {
    e.preventDefault();
    if (q.trim()) navigate(`/search?q=${encodeURIComponent(q.trim())}`);
  };

  if (pinLocked) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg p-6 pt-[max(1.5rem,var(--sat))] pb-[max(1.5rem,var(--sab))]">
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
          <Logo className="mx-auto h-20 w-20" />
          <h1 className="text-center text-base font-semibold">Unlock</h1>
          <input
            type="password"
            inputMode="numeric"
            autoComplete="off"
            placeholder="PIN"
            value={pin}
            onChange={(e) => setPin(e.target.value)}
            className="w-full"
          />
          {pinErr ? <p className="text-xs text-danger">{pinErr}</p> : null}
          <button type="submit" className="btn-green tap w-full rounded-full px-3 text-sm">
            Continue
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh bg-bg">
      <aside className="hidden w-[232px] shrink-0 flex-col border-r border-line bg-raised/80 md:flex">
        <Link to="/" className="flex items-center gap-2 px-3 py-4">
          <Logo className="h-12 w-12" />
          <span className="text-sm font-bold tracking-wide">View<span className="text-accent">Dock</span></span>
        </Link>
        {subnav ? (
          <SubnavSidebar nav={subnav} linkClass={sideLink} />
        ) : me?.is_admin && inAdmin ? (
          <nav aria-label="Admin" className="flex-1 overflow-y-auto px-2 pt-3">
            <Link to="/" className={sideLink}>
              <ArrowLeft className="h-4 w-4 shrink-0" />
              Back to app
            </Link>
            {ADMIN_SECTIONS.map((section) => (
              <div key={section.label || "top"} className="mt-3 space-y-0.5">
                {section.label ? (
                  <p className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wide text-dim">{section.label}</p>
                ) : null}
                {section.links.map((it) => (
                  <NavLink key={it.to} to={it.to} end={it.end} className={({ isActive }) => cn(sideLink, isActive && "bg-overlay text-ink")}>
                    <it.icon className="h-4 w-4 shrink-0" />
                    {it.label}
                  </NavLink>
                ))}
              </div>
            ))}
          </nav>
        ) : (
          <nav className="flex-1 space-y-0.5 px-2 pt-3">
            {nav.map((it) => (
              <NavLink key={it.to} to={it.to} end={it.end} className={({ isActive }) => cn(sideLink, isActive && "bg-overlay text-ink")}>
                <it.icon className="h-4 w-4 shrink-0" />
                {it.label}
              </NavLink>
            ))}
            {me?.is_admin ? (
              <NavLink to="/admin" className={({ isActive }) => cn(sideLink, isActive && "bg-overlay text-ink")}>
                <Shield className="h-4 w-4 shrink-0" />
                Admin
              </NavLink>
            ) : null}
          </nav>
        )}
        <div className="border-t border-line p-2">
          <NavLink to="/profile" className={({ isActive }) => cn(sideLink, isActive && "bg-overlay text-ink")}>
            <Settings className="h-4 w-4 shrink-0" />
            <span className="truncate">{me?.display_name || me?.username || "Profile"}</span>
          </NavLink>
          <button
            type="button"
            className={cn(sideLink, "w-full")}
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
        <header
          className="sticky top-0 z-30 flex items-center gap-3 border-b border-line bg-bg/80 px-4 backdrop-blur"
          style={{
            paddingTop: "max(0.5rem, var(--sat))",
            minHeight: "calc(var(--nav-h) + var(--sat))",
          }}
        >
          <Link to="/" className="shrink-0 md:hidden" aria-label="ViewDock home">
            <Logo className="h-9 w-9" />
          </Link>
          <form onSubmit={onSearch} className="flex min-w-0 flex-1 items-center gap-2">
            <Search size={16} className="shrink-0 text-dim" />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search movies and TV"
              enterKeyHint="search"
              className="h-11 w-full max-w-md border-0 bg-transparent px-0"
            />
          </form>
        </header>
        <ConnectivityBanner />
        <main className="px-4 py-5 md:px-8 pb-[calc(var(--tab-h)+var(--sab)+1rem)] md:pb-5">
          <Outlet />
        </main>
      </div>

      <nav
        className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-raised/95 backdrop-blur md:hidden"
        style={{ paddingBottom: "var(--sab)" }}
      >
        <div className="grid h-[var(--tab-h)] grid-cols-5">
          {nav.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              className={({ isActive }) =>
                cn(
                  "flex flex-col items-center justify-center gap-0.5 text-[10px] text-dim",
                  isActive && "text-accent",
                )
              }
            >
              <it.icon className="h-5 w-5" />
              {it.label}
            </NavLink>
          ))}
          <NavLink
            to="/profile"
            className={({ isActive }) =>
              cn(
                "flex flex-col items-center justify-center gap-0.5 text-[10px] text-dim",
                isActive && "text-accent",
              )
            }
          >
            <Settings className="h-5 w-5" />
            Me
          </NavLink>
        </div>
      </nav>
    </div>
  );
}
