import { NavLink, Outlet } from "react-router";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/format";
import { useUploads } from "@/store/uploads";
import { ADMIN_LINKS } from "./adminNav";
import { SubnavPills, useAdminSubnav, useHashScroll } from "./AdminSubnav";

export function AdminLayout() {
  const jobs = useUploads((s) => s.jobs);
  const active = jobs.filter((j) => j.status === "uploading" || j.status === "processing" || j.status === "queued");
  const subnav = useAdminSubnav(true);
  useHashScroll();

  return (
    <div>
      {/* On wider screens these links replace the main sidebar. */}
      {subnav ? (
        <SubnavPills nav={subnav} />
      ) : (
        <nav aria-label="Admin" className="h-scroll mb-4 flex gap-1 pb-1 text-sm md:hidden">
          {ADMIN_LINKS.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              end={l.end}
              className={({ isActive }) => cn("tap shrink-0 rounded-full px-3 text-dim", isActive && "bg-overlay text-ink")}
            >
              {l.label}
            </NavLink>
          ))}
        </nav>
      )}
      {active.length ? (
        <div className="mb-4 rounded-md border border-line bg-raised px-3 py-2 text-xs text-dim">
          {active.map((j) => (
            <p key={j.localId}>
              {j.filename} · {j.status}
              {j.size ? ` · ${formatBytes(j.offset)} / ${formatBytes(j.size)}` : ""}
            </p>
          ))}
        </div>
      ) : null}
      <Outlet />
    </div>
  );
}
