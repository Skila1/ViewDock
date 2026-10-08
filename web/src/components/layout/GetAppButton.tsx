import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { MonitorDown } from "lucide-react";
import { api } from "@/api/api";
import { desktopApp } from "@/lib/desktop";

/**
 * ViewDock for Windows as the server offers it, for Windows browsers only:
 * null on other systems, inside the app, or when the app is turned off.
 */
function useWindowsApp() {
  const onWindows = typeof navigator !== "undefined" && /Windows/i.test(navigator.userAgent);
  const show = onWindows && !desktopApp();
  const info = useQuery({ queryKey: ["desktop-info"], queryFn: api.desktopInfo, enabled: show, staleTime: 60_000 });
  return show && info.data?.available ? info.data : null;
}

/** Offers ViewDock for Windows in the top bar. It leads to the download on Profile. */
export function GetAppButton() {
  const app = useWindowsApp();
  if (!app) return null;
  return (
    <Link
      to="/profile#desktop-app"
      className="tap ml-auto flex shrink-0 items-center gap-2 rounded-md border border-line px-3 py-1.5 text-sm text-ink hover:border-accent/50 hover:bg-overlay"
      title="Download ViewDock for Windows"
    >
      <MonitorDown className="h-4 w-4 text-accent" />
      <span className="hidden sm:inline">Get the app</span>
    </Link>
  );
}

/**
 * "Download for Windows" in the sidebar: downloads the installer at once
 * when the server has it built, and otherwise opens the download on
 * Profile, which waits while the server builds it.
 */
export function SidebarDownload({ collapsed, linkClass }: { collapsed: boolean; linkClass: string }) {
  const app = useWindowsApp();
  if (!app?.windows) return null;
  const label = "Download for Windows";
  const icon = <MonitorDown className="h-4 w-4 shrink-0 text-accent" />;
  return (
    <div className="px-2 pb-1">
      {app.windows.ready ? (
        <a href={app.windows.url} download className={linkClass} title={collapsed ? label : undefined}>
          {icon}
          {collapsed ? null : <span className="truncate">{label}</span>}
        </a>
      ) : (
        <Link to="/profile#desktop-app" className={linkClass} title={collapsed ? label : undefined}>
          {icon}
          {collapsed ? null : <span className="truncate">{label}</span>}
        </Link>
      )}
    </div>
  );
}
