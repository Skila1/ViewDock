import { useEffect, type ReactNode } from "react";
import { Navigate, Outlet, Route, Routes, useLocation } from "react-router";
import { AppShell } from "@/components/layout/AppShell";
import { GuestShell } from "@/components/layout/GuestShell";
import { AdminLayout } from "@/features/admin/AdminLayout";
import { InspectorPage } from "@/features/admin/InspectorPage";
import { DiagnosticsPage } from "@/features/admin/DiagnosticsPage";
import { StreamsPage } from "@/features/admin/StreamsPage";
import { DiscordPage } from "@/features/admin/DiscordPage";
import { NodesPage } from "@/features/admin/NodesPage";
import { MediaSourcesPage } from "@/features/admin/MediaSourcesPage";
import { MediaLibrariesPage } from "@/features/admin/MediaLibrariesPage";
import { MediaTitlesPage } from "@/features/admin/MediaTitlesPage";
import { WatchPartiesPage } from "@/features/admin/WatchPartiesPage";
import { BackupsPage } from "@/features/admin/BackupsPage";
import { SettingsPage } from "@/features/admin/SettingsPage";
import { GrantsPage } from "@/features/admin/GrantsPage";
import { RolesPage } from "@/features/admin/RolesPage";
import { UpdatesPage } from "@/features/admin/UpdatesPage";
import { APIKeysPage } from "@/features/admin/APIKeysPage";
import { LogsPage } from "@/features/admin/LogsPage";
import { AuditPage } from "@/features/admin/AuditPage";
import { OfflineVaultPage } from "@/pages/OfflineVaultPage";
import { DeviceTestPage } from "@/pages/DeviceTestPage";
import { HouseholdsPage } from "@/features/admin/HouseholdsPage";
import { UploadsPage } from "@/features/admin/UploadsPage";
import { UsersPage } from "@/features/admin/UsersPage";
import { AdminPage } from "@/pages/AdminPage";
import { ConnectedPage } from "@/pages/ConnectedPage";
import { HomePage } from "@/pages/HomePage";
import { LoginPage } from "@/pages/LoginPage";
import { ProfilePage } from "@/pages/ProfilePage";
import { MovieDetailPage } from "@/pages/MovieDetailPage";
import { SeriesDetailPage } from "@/pages/SeriesDetailPage";
import { SetupPage } from "@/pages/SetupPage";
import { SharePage } from "@/pages/SharePage";
import { TogetherPage } from "@/pages/TogetherPage";
import { WatchPage } from "@/pages/WatchPage";
import { bindJourneyLifecycle, report, startJourney } from "@/lib/journey";
import { useAuth } from "@/store/auth";
import { onConnectivity } from "@/api/client";
import { ActivityGate } from "@/features/activity/ActivityGate";
import { ActivityPage } from "@/pages/ActivityPage";
import { isDiscordActivity } from "@/lib/discordActivity";

function EmbedGate({ children }: { children: ReactNode }) {
  return isDiscordActivity() ? <ActivityGate>{children}</ActivityGate> : children;
}

function BootGate({ children }: { children: ReactNode }) {
  const { ready, boot, error, offline } = useAuth();
  useEffect(() => {
    void boot();
  }, [boot]);
  useEffect(() => {
    if (!offline) return;
    return onConnectivity((c) => {
      if (c.api) void boot();
    });
  }, [offline, boot]);
  if (!ready) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-dim">Starting…</div>;
  }
  if (error && offline) {
    return (
      <div className="flex min-h-dvh flex-col items-center justify-center gap-3 px-6 text-center">
        <p className="text-base font-medium">ViewDock server unreachable</p>
        <p className="max-w-sm text-sm text-dim">
          No saved data is available on this device yet. ViewDock will reconnect automatically when the server is back.
        </p>
        <button type="button" className="rounded-md border border-line px-3 py-1.5 text-sm" onClick={() => void boot()}>
          Try again
        </button>
      </div>
    );
  }
  if (error) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-danger">{error}</div>;
  }
  return children;
}

function SetupRedirect() {
  const { system } = useAuth();
  const location = useLocation();
  const publicPath =
    location.pathname.startsWith("/setup") ||
    location.pathname.startsWith("/login") ||
    location.pathname.startsWith("/s/");
  if (system?.setup_needed && !publicPath) {
    return <Navigate to="/setup" replace />;
  }
  return <Outlet />;
}

function RequireAuth() {
  const { me, system } = useAuth();
  const location = useLocation();
  if (system?.setup_needed) return <Navigate to="/setup" replace />;
  if (!me) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <Outlet />;
}

/** Old /search?q= links open the same search on the home page. */
function SearchRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/${search}`} replace />;
}

function RequireAdmin() {
  const { me } = useAuth();
  if (!me?.is_admin) return <Navigate to="/" replace />;
  return <Outlet />;
}

function JourneyReporter() {
  const location = useLocation();
  useEffect(() => {
    startJourney();
    return bindJourneyLifecycle();
  }, []);
  useEffect(() => {
    const path = location.pathname;
    report("route", { path, search: location.search });
    const movie = path.match(/^\/movies\/([^/]+)/);
    if (movie) report("movie_open", { id: movie[1] });
    const series = path.match(/^\/tv\/([^/]+)/);
    if (series) report("series_open", { id: series[1] });
    const watch = path.match(/^\/watch\/(movie|episode)\/([^/]+)/);
    if (watch) report("watch_open", { kind: watch[1], id: watch[2], t: new URLSearchParams(location.search).get("t") });
  }, [location.pathname, location.search]);
  return null;
}

export function App() {
  return (
    <>
      <JourneyReporter />
      <EmbedGate>
      <BootGate>
      <Routes>
        <Route element={<SetupRedirect />}>
          <Route path="/setup" element={<SetupPage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="/s/:token" element={<GuestShell />}>
            <Route index element={<SharePage />} />
            <Route path="together/:code" element={<TogetherPage guest />} />
          </Route>
          <Route element={<RequireAuth />}>
            <Route element={<AppShell />}>
              <Route path="/" element={<HomePage />} />
              <Route path="/movies" element={<Navigate to="/?type=movie" replace />} />
              <Route path="/tv" element={<Navigate to="/?type=series" replace />} />
              <Route path="/movies/:id" element={<MovieDetailPage />} />
              <Route path="/tv/:id" element={<SeriesDetailPage />} />
              <Route path="/search" element={<SearchRedirect />} />
              <Route path="/profile" element={<ProfilePage />} />
              <Route path="/offline" element={<OfflineVaultPage />} />
              <Route path="/device-test" element={<DeviceTestPage />} />
              <Route path="/settings/connected" element={<ConnectedPage />} />
              <Route element={<RequireAdmin />}>
                <Route path="/admin" element={<AdminLayout />}>
                  <Route index element={<AdminPage />} />
                  <Route path="streams" element={<StreamsPage />} />
                  <Route path="streams/:sessionId" element={<InspectorPage />} />
                  <Route path="diagnostics" element={<DiagnosticsPage />} />
                  <Route path="nodes" element={<NodesPage />} />
                  <Route path="media" element={<MediaLibrariesPage />} />
                  <Route path="media/titles" element={<MediaTitlesPage />} />
                  <Route path="media/uploads" element={<UploadsPage />} />
                  <Route path="media/access" element={<GrantsPage />} />
                  <Route path="media/sources" element={<MediaSourcesPage />} />
                  <Route path="media-sources" element={<Navigate to="/admin/media/sources" replace />} />
                  <Route path="uploads" element={<Navigate to="/admin/media/uploads" replace />} />
                  <Route path="grants" element={<Navigate to="/admin/media/access" replace />} />
                  <Route path="watch-parties" element={<WatchPartiesPage />} />
                  <Route path="backups" element={<BackupsPage />} />
                  <Route path="users" element={<UsersPage />} />
                  <Route path="households" element={<HouseholdsPage />} />
                  <Route path="roles" element={<RolesPage />} />
                  <Route path="settings/:section?" element={<SettingsPage />} />
                  <Route path="api-keys" element={<APIKeysPage />} />
                  <Route path="logs" element={<LogsPage />} />
                  <Route path="audit" element={<AuditPage />} />
                  <Route path="discord/:section?" element={<DiscordPage />} />
                  <Route path="updates" element={<UpdatesPage />} />
                </Route>
              </Route>
            </Route>
            <Route path="/watch/movie/:id" element={<WatchPage kind="movie" />} />
            <Route path="/watch/episode/:id" element={<WatchPage kind="episode" />} />
            <Route path="/together/:code" element={<TogetherPage />} />
            <Route path="/activity" element={<ActivityPage />} />
          </Route>
        </Route>
      </Routes>
    </BootGate>
      </EmbedGate>
    </>
  );
}
