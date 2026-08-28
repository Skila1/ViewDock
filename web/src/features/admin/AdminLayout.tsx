import { NavLink, Outlet } from "react-router";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/format";
import { useUploads } from "@/store/uploads";

const links = [
  { to: "/admin", label: "Overview", end: true },
  { to: "/admin/uploads", label: "Uploads" },
  { to: "/admin/streams", label: "Streams" },
  { to: "/admin/users", label: "Users" },
  { to: "/admin/roles", label: "Groups" },
  { to: "/admin/grants", label: "Grants" },
  { to: "/admin/settings", label: "Settings" },
  { to: "/admin/discord", label: "Discord" },
  { to: "/admin/updates", label: "Updates" },
];

export function AdminLayout() {
  const jobs = useUploads((s) => s.jobs);
  const active = jobs.filter((j) => j.status === "uploading" || j.status === "processing" || j.status === "queued");

  return (
    <div>
      <nav className="mb-4 flex flex-wrap gap-3 text-sm">
        {links.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            end={l.end}
            className={({ isActive }) => cn("text-dim", isActive && "text-ink")}
          >
            {l.label}
          </NavLink>
        ))}
      </nav>
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
