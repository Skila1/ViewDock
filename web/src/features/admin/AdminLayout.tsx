import { NavLink, Outlet } from "react-router";
import { cn } from "@/lib/cn";

const links = [
  { to: "/admin", label: "Overview", end: true },
  { to: "/admin/streams", label: "Streams" },
  { to: "/admin/users", label: "Users" },
  { to: "/admin/roles", label: "Groups" },
  { to: "/admin/grants", label: "Grants" },
  { to: "/admin/settings", label: "Settings" },
  { to: "/admin/discord", label: "Discord" },
  { to: "/admin/updates", label: "Updates" },
];

export function AdminLayout() {
  return (
    <div>
      <nav className="mb-4 flex gap-3 text-sm">
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
      <Outlet />
    </div>
  );
}
