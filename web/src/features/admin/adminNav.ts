import type { LucideIcon } from "lucide-react";
import {
  Activity,
  Archive,
  Bot,
  Clapperboard,
  Command,
  Download,
  Film,
  HardDriveUpload,
  History,
  Home,
  KeyRound,
  LayoutDashboard,
  Library,
  Mail,
  MonitorPlay,
  Network,
  Radio,
  RadioTower,
  ScrollText,
  Server,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
  Stethoscope,
  UserPlus,
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
      { to: "/admin/media", label: "Media", icon: Clapperboard },
      { to: "/admin/streams", label: "Streams", icon: RadioTower },
    ],
  },
  {
    label: "People",
    links: [
      { to: "/admin/users", label: "Users", icon: Users },
      { to: "/admin/roles", label: "Groups", icon: UsersRound },
      { to: "/admin/households", label: "Households", icon: Home },
      { to: "/admin/watch-parties", label: "Watch parties", icon: Radio },
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
      { to: "/admin/audit", label: "Audit", icon: ShieldAlert },
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

/** Admin pages with enough content to get their own sidebar; each link is a separate page. */
export type AdminSubnav = { base: string; title: string; sections: AdminSection[] };

export const MEDIA_SUBNAV: AdminSubnav = {
  base: "/admin/media",
  title: "Media",
  sections: [
    {
      label: "Library",
      links: [
        { to: "/admin/media", label: "Libraries", icon: Library, end: true },
        { to: "/admin/media/titles", label: "Titles", icon: Film },
      ],
    },
    {
      label: "Content",
      links: [
        { to: "/admin/media/uploads", label: "Uploads", icon: HardDriveUpload },
        { to: "/admin/media/access", label: "Library access", icon: ShieldCheck },
      ],
    },
    { label: "Sources", links: [{ to: "/admin/media/sources", label: "Jellyfin servers", icon: Server }] },
  ],
};

export const DISCORD_SUBNAV: AdminSubnav = {
  base: "/admin/discord",
  title: "Discord",
  sections: [
    { label: "Status", links: [{ to: "/admin/discord", label: "Diagnostics", icon: Stethoscope, end: true }] },
    {
      label: "Sign-in",
      links: [
        { to: "/admin/discord/auth", label: "Authentication", icon: KeyRound },
        { to: "/admin/discord/registration", label: "Registration", icon: UserPlus },
      ],
    },
    {
      label: "Bot",
      links: [
        { to: "/admin/discord/bot", label: "Bot configuration", icon: Bot },
        { to: "/admin/discord/commands", label: "Slash commands", icon: Command },
        { to: "/admin/discord/invites", label: "Party invitations", icon: Mail },
      ],
    },
    { label: "Activity", links: [{ to: "/admin/discord/activity", label: "Discord Activity", icon: MonitorPlay }] },
  ],
};

/** Settings categories come from the server, so its sidebar is built from them. */
export function settingsSubnav(categories: { slug: string; label: string }[]): AdminSubnav {
  return {
    base: "/admin/settings",
    title: "Settings",
    sections: [
      {
        label: "Categories",
        links: categories.map((c) => ({ to: `/admin/settings/${c.slug}`, label: c.label, icon: SlidersHorizontal })),
      },
      { label: "Versions", links: [{ to: "/admin/settings/history", label: "History", icon: History }] },
    ],
  };
}

export function underPath(pathname: string, base: string): boolean {
  return pathname === base || pathname.startsWith(`${base}/`);
}
