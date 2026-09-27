import type { LucideIcon } from "lucide-react";
import {
  Activity,
  Archive,
  Bot,
  Download,
  HardDriveUpload,
  Home,
  KeyRound,
  LayoutDashboard,
  Library,
  Network,
  RadioTower,
  ScrollText,
  Server,
  SlidersHorizontal,
  Users,
  UsersRound,
} from "lucide-react";

export type AdminLink = { to: string; label: string; icon: LucideIcon; end?: boolean };
export type AdminSection = { label: string; links: AdminLink[] };

/** Every admin page, grouped as shown in the admin sidebar. */
export const ADMIN_SECTIONS: AdminSection[] = [
  { label: "", links: [{ to: "/admin", label: "Overview", icon: LayoutDashboard, end: true }] },
  {
    label: "Media",
    links: [
      { to: "/admin/media-sources", label: "Media sources", icon: Server },
      { to: "/admin/uploads", label: "Uploads", icon: HardDriveUpload },
      { to: "/admin/streams", label: "Streams", icon: RadioTower },
    ],
  },
  {
    label: "People",
    links: [
      { to: "/admin/users", label: "Users", icon: Users },
      { to: "/admin/roles", label: "Groups", icon: UsersRound },
      { to: "/admin/households", label: "Households", icon: Home },
      { to: "/admin/grants", label: "Library access", icon: Library },
    ],
  },
  { label: "Integrations", links: [{ to: "/admin/discord", label: "Discord", icon: Bot }] },
  {
    label: "System",
    links: [
      { to: "/admin/settings", label: "Settings", icon: SlidersHorizontal },
      { to: "/admin/nodes", label: "Nodes", icon: Network },
      { to: "/admin/diagnostics", label: "Resilience", icon: Activity },
      { to: "/admin/backups", label: "Backups", icon: Archive },
      { to: "/admin/logs", label: "Logs", icon: ScrollText },
      { to: "/admin/api-keys", label: "API keys", icon: KeyRound },
      { to: "/admin/updates", label: "Updates", icon: Download },
    ],
  },
];

export const ADMIN_LINKS: AdminLink[] = ADMIN_SECTIONS.flatMap((s) => s.links);

export function isAdminPath(pathname: string): boolean {
  return pathname === "/admin" || pathname.startsWith("/admin/");
}
