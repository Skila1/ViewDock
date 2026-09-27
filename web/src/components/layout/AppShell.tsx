import { useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { ArrowLeft, Download, Home, PanelLeftClose, PanelLeftOpen, Settings, Shield } from "lucide-react";
import { Logo } from "@/components/brand/Logo";
import { useAuth } from "@/store/auth";
import { cn } from "@/lib/cn";
import { ConnectivityBanner } from "@/components/layout/ConnectivityBanner";
import { ProfileMenu } from "@/components/layout/ProfileMenu";
import { HeaderSearch } from "@/components/browse/HeaderSearch";
import { ADMIN_SECTIONS, isAdminPath } from "@/features/admin/adminNav";
import { SubnavSidebar, useAdminSubnav } from "@/features/admin/AdminSubnav";

const sideLink = "flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-dim hover:bg-overlay hover:text-ink";

const nav = [
  { to: "/", label: "Home", icon: Home, end: true },
  { to: "/offline", label: "Offline", icon: Download },
];

const COLLAPSE_KEY = "vd.sidebar.collapsed";

function useSidebarCollapsed(): [boolean, () => void] {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(COLLAPSE_KEY) === "1";
    } catch {
      return false;
    }
  });
  const toggle = () =>
    setCollapsed((v) => {
      try {
        localStorage.setItem(COLLAPSE_KEY, v ? "0" : "1");
      } catch {
        /* private mode: the choice lasts for this page only */
      }
      return !v;
    });
  return [collapsed, toggle];
}

export function AppShell() {
  const { me, pinLocked, unlockPin } = useAuth();
  const inAdmin = isAdminPath(useLocation().pathname);
  const subnav = useAdminSubnav(Boolean(me?.is_admin && inAdmin));
  const [collapsed, toggleCollapsed] = useSidebarCollapsed();
  const [pin, setPin] = useState("");
  const [pinErr, setPinErr] = useState("");
  const link = cn(sideLink, collapsed && "justify-center px-0");
  const label = (text: string) => (collapsed ? <span className="sr-only">{text}</span> : text);

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
      <aside
        className={cn(
          "sticky top-0 hidden h-dvh shrink-0 flex-col border-r border-line bg-raised/80 transition-[width] duration-150 md:flex",
          collapsed ? "w-16" : "w-[232px]",
        )}
      >
        <Link to="/" className={cn("flex items-center gap-2 py-4", collapsed ? "justify-center px-1" : "px-3")} aria-label="ViewDock home">
          <Logo className={collapsed ? "h-10 w-10" : "h-12 w-12"} />
          {!collapsed ? (
            <span className="text-sm font-bold tracking-wide">
              View<span className="text-accent">Dock</span>
            </span>
          ) : null}
        </Link>
        {subnav ? (
          <SubnavSidebar nav={subnav} linkClass={link} collapsed={collapsed} />
        ) : me?.is_admin && inAdmin ? (
          <nav aria-label="Admin" className="flex-1 overflow-y-auto px-2 pt-3">
            <Link to="/" className={link} title={collapsed ? "Back to app" : undefined}>
              <ArrowLeft className="h-4 w-4 shrink-0" />
              {label("Back to app")}
            </Link>
            {ADMIN_SECTIONS.map((section) => (
              <div key={section.label || "top"} className="mt-3 space-y-0.5">
                {section.label && !collapsed ? (
                  <p className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wide text-dim">{section.label}</p>
                ) : null}
                {section.links.map((it) => (
                  <NavLink
                    key={it.to}
                    to={it.to}
                    end={it.end}
                    title={collapsed ? it.label : undefined}
                    className={({ isActive }) => cn(link, isActive && "bg-overlay text-ink")}
                  >
                    <it.icon className="h-4 w-4 shrink-0" />
                    {label(it.label)}
                  </NavLink>
                ))}
              </div>
            ))}
          </nav>
        ) : (
          <nav aria-label="Main" className="flex-1 space-y-0.5 overflow-y-auto px-2 pt-3">
            {nav.map((it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.end}
                title={collapsed ? it.label : undefined}
                className={({ isActive }) => cn(link, isActive && "bg-overlay text-ink")}
              >
                <it.icon className="h-4 w-4 shrink-0" />
                {label(it.label)}
              </NavLink>
            ))}
            {me?.is_admin ? (
              <NavLink to="/admin" title={collapsed ? "Admin" : undefined} className={({ isActive }) => cn(link, isActive && "bg-overlay text-ink")}>
                <Shield className="h-4 w-4 shrink-0" />
                {label("Admin")}
              </NavLink>
            ) : null}
          </nav>
        )}
        <ProfileMenu collapsed={collapsed} linkClass={sideLink} />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header
          className="sticky top-0 z-30 flex items-center gap-3 border-b border-line bg-bg/80 px-4 backdrop-blur"
          style={{
            paddingTop: "max(0.5rem, var(--sat))",
            minHeight: "calc(var(--nav-h) + var(--sat))",
          }}
        >
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="tap hidden w-9 shrink-0 items-center justify-center rounded-md text-dim hover:bg-overlay hover:text-ink md:flex"
          >
            {collapsed ? <PanelLeftOpen className="h-4 w-4" /> : <PanelLeftClose className="h-4 w-4" />}
          </button>
          <Link to="/" className="shrink-0 md:hidden" aria-label="ViewDock home">
            <Logo className="h-9 w-9" />
          </Link>
          <HeaderSearch />
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
        <div className={cn("grid h-[var(--tab-h)]", me?.is_admin ? "grid-cols-4" : "grid-cols-3")}>
          {[...nav, ...(me?.is_admin ? [{ to: "/admin", label: "Admin", icon: Shield, end: false }] : []), { to: "/profile", label: "Me", icon: Settings, end: false }].map(
            (it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.end}
                className={({ isActive }) =>
                  cn("flex flex-col items-center justify-center gap-0.5 text-[10px] text-dim", isActive && "text-accent")
                }
              >
                <it.icon className="h-5 w-5" />
                {it.label}
              </NavLink>
            ),
          )}
        </div>
      </nav>
    </div>
  );
}
