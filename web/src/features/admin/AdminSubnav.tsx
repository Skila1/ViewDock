import { useMemo } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { api } from "@/api/api";
import { cn } from "@/lib/cn";
import { DISCORD_SUBNAV, MEDIA_SUBNAV, settingsSubnav, underPath, type AdminSubnav } from "./adminNav";
import { settingsGroups, settingsSlug } from "./SettingsPage";

/** The third-level sidebar for the current admin page, or null when the page has none. */
export function useAdminSubnav(enabled: boolean): AdminSubnav | null {
  const { pathname } = useLocation();
  const inSettings = enabled && underPath(pathname, "/admin/settings");
  const config = useQuery({ queryKey: ["admin-config"], queryFn: api.getConfig, enabled: inSettings });
  const categories = useMemo(
    () => settingsGroups(config.data?.settings ?? []).map(([c]) => ({ slug: settingsSlug(c), label: c })),
    [config.data],
  );
  if (!enabled) return null;
  if (underPath(pathname, MEDIA_SUBNAV.base)) return MEDIA_SUBNAV;
  if (underPath(pathname, DISCORD_SUBNAV.base)) return DISCORD_SUBNAV;
  if (inSettings) return settingsSubnav(categories);
  return null;
}

export function SubnavSidebar({ nav, linkClass, collapsed }: { nav: AdminSubnav; linkClass: string; collapsed?: boolean }) {
  return (
    <nav aria-label={`${nav.title} sections`} className="flex-1 overflow-y-auto px-2 pt-3">
      <Link to="/admin" className={linkClass} title={collapsed ? "Back to admin" : undefined}>
        <ArrowLeft className="h-4 w-4 shrink-0" />
        {collapsed ? <span className="sr-only">Back to admin</span> : "Back to admin"}
      </Link>
      {!collapsed ? <p className="mt-3 px-3 text-sm font-semibold">{nav.title}</p> : null}
      {nav.sections.map((section) =>
        section.links.length ? (
          <div key={section.label} className="mt-3 space-y-0.5">
            {!collapsed ? <p className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wide text-dim">{section.label}</p> : null}
            {section.links.map((it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end={it.end}
                title={collapsed ? it.label : undefined}
                className={({ isActive }) => cn(linkClass, isActive && "bg-overlay text-ink")}
              >
                <it.icon className="h-4 w-4 shrink-0" />
                {collapsed ? <span className="sr-only">{it.label}</span> : <span className="truncate">{it.label}</span>}
              </NavLink>
            ))}
          </div>
        ) : null,
      )}
    </nav>
  );
}

export function SubnavPills({ nav }: { nav: AdminSubnav }) {
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
          <NavLink key={l.to} to={l.to} end={l.end} className={({ isActive }) => cn(pill, isActive && "bg-overlay text-ink")}>
            {l.label}
          </NavLink>
        ))}
    </nav>
  );
}
