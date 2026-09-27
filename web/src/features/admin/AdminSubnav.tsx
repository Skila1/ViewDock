import { useEffect, useMemo } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { api } from "@/api/api";
import { cn } from "@/lib/cn";
import { DISCORD_SUBNAV, MEDIA_SUBNAV, settingsSubnav, underPath, type AdminLink, type AdminSubnav } from "./adminNav";
import { cardId, settingsGroups } from "./SettingsPage";

/** The third-level sidebar for the current admin page, or null when the page has none. */
export function useAdminSubnav(enabled: boolean): AdminSubnav | null {
  const { pathname } = useLocation();
  const inSettings = enabled && underPath(pathname, "/admin/settings");
  const config = useQuery({ queryKey: ["admin-config"], queryFn: api.getConfig, enabled: inSettings });
  const categories = useMemo(
    () => settingsGroups(config.data?.settings ?? []).map(([c]) => ({ id: cardId(c), label: c })),
    [config.data],
  );
  if (!enabled) return null;
  if (underPath(pathname, MEDIA_SUBNAV.base)) return MEDIA_SUBNAV;
  if (underPath(pathname, DISCORD_SUBNAV.base)) return DISCORD_SUBNAV;
  if (inSettings) return settingsSubnav(categories);
  return null;
}

function linkHash(to: string): string {
  const i = to.indexOf("#");
  return i < 0 ? "" : to.slice(i);
}

/** Hash links are active when their anchor is the current one; the first is active with no anchor. */
export function useSubnavActive(nav: AdminSubnav) {
  const { hash } = useLocation();
  const firstHash = nav.sections.flatMap((s) => s.links).find((l) => linkHash(l.to))?.to;
  return (link: AdminLink, routeActive: boolean) => {
    const h = linkHash(link.to);
    if (!h) return routeActive;
    return hash ? hash === h : link.to === firstHash;
  };
}

export function SubnavSidebar({ nav, linkClass }: { nav: AdminSubnav; linkClass: string }) {
  const isActive = useSubnavActive(nav);
  return (
    <nav aria-label={`${nav.title} sections`} className="flex-1 overflow-y-auto px-2 pt-3">
      <Link to="/admin" className={linkClass}>
        <ArrowLeft className="h-4 w-4 shrink-0" />
        Back to admin
      </Link>
      <p className="mt-3 px-3 text-sm font-semibold">{nav.title}</p>
      {nav.sections.map((section) =>
        section.links.length ? (
          <div key={section.label} className="mt-3 space-y-0.5">
            <p className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wide text-dim">{section.label}</p>
            {section.links.map((it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.end}
                className={({ isActive: route }) => cn(linkClass, isActive(it, route) && "bg-overlay text-ink")}
              >
                <it.icon className="h-4 w-4 shrink-0" />
                <span className="truncate">{it.label}</span>
              </NavLink>
            ))}
          </div>
        ) : null,
      )}
    </nav>
  );
}

export function SubnavPills({ nav }: { nav: AdminSubnav }) {
  const isActive = useSubnavActive(nav);
  const pill = "tap shrink-0 rounded-full px-3 text-dim";
  return (
    <nav aria-label={`${nav.title} sections`} className="h-scroll mb-4 flex gap-1 pb-1 text-sm md:hidden">
      <Link to="/admin" className={cn(pill, "inline-flex items-center gap-1")}>
        <ArrowLeft className="h-3.5 w-3.5" />
        Admin
      </Link>
      {nav.sections
        .flatMap((s) => s.links)
        .map((l) => (
          <NavLink key={l.to} to={l.to} end={l.end} className={({ isActive: route }) => cn(pill, isActive(l, route) && "bg-overlay text-ink")}>
            {l.label}
          </NavLink>
        ))}
    </nav>
  );
}

/** Scrolls to the `#id` in the URL once that element has rendered, on every navigation. */
export function useHashScroll() {
  const { hash, key } = useLocation();
  useEffect(() => {
    if (!hash) return;
    const id = decodeURIComponent(hash.slice(1));
    let frame = 0;
    const started = performance.now();
    const tick = () => {
      const el = document.getElementById(id);
      if (el) {
        el.scrollIntoView({ block: "start", behavior: "smooth" });
        return;
      }
      if (performance.now() - started < 3000) frame = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(frame);
  }, [hash, key]);
}
