import { useEffect, type ReactNode } from "react";
import { Navigate, Outlet, Route, Routes, useLocation } from "react-router";
import { AppShell } from "@/components/layout/AppShell";
import { GuestShell } from "@/components/layout/GuestShell";
import { AdminLayout } from "@/features/admin/AdminLayout";
import { InspectorPage } from "@/features/admin/InspectorPage";
import { StreamsPage } from "@/features/admin/StreamsPage";
import { DiscordPage } from "@/features/admin/DiscordPage";
import { GrantsPage } from "@/features/admin/GrantsPage";
import { RolesPage } from "@/features/admin/RolesPage";
import { UpdatesPage } from "@/features/admin/UpdatesPage";
import { UsersPage } from "@/features/admin/UsersPage";
import { AdminPage } from "@/pages/AdminPage";
import { ConnectedPage } from "@/pages/ConnectedPage";
import { HomePage } from "@/pages/HomePage";
import { LoginPage } from "@/pages/LoginPage";
import { ProfilePage } from "@/pages/ProfilePage";
import { MovieDetailPage } from "@/pages/MovieDetailPage";
import { SearchPage } from "@/pages/SearchPage";
import { SeriesDetailPage } from "@/pages/SeriesDetailPage";
import { SetupPage } from "@/pages/SetupPage";
import { SharePage } from "@/pages/SharePage";
import { TogetherPage } from "@/pages/TogetherPage";
import { WatchPage } from "@/pages/WatchPage";
import { useAuth } from "@/store/auth";

function BootGate({ children }: { children: ReactNode }) {
  const { ready, boot, error } = useAuth();
  useEffect(() => {
    void boot();
  }, [boot]);
  if (!ready) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-dim">Starting…</div>;
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

function RequireAdmin() {
  const { me } = useAuth();
  if (!me?.is_admin) return <Navigate to="/" replace />;
  return <Outlet />;
}

export function App() {
  return (
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
              <Route path="/movies" element={<HomePage filter="movies" />} />
              <Route path="/tv" element={<HomePage filter="tv" />} />
              <Route path="/movies/:id" element={<MovieDetailPage />} />
              <Route path="/tv/:id" element={<SeriesDetailPage />} />
              <Route path="/search" element={<SearchPage />} />
              <Route path="/profile" element={<ProfilePage />} />
              <Route path="/settings/connected" element={<ConnectedPage />} />
              <Route element={<RequireAdmin />}>
                <Route path="/admin" element={<AdminLayout />}>
                  <Route index element={<AdminPage />} />
                  <Route path="streams" element={<StreamsPage />} />
                  <Route path="streams/:sessionId" element={<InspectorPage />} />
                  <Route path="users" element={<UsersPage />} />
                  <Route path="roles" element={<RolesPage />} />
                  <Route path="grants" element={<GrantsPage />} />
                  <Route path="discord" element={<DiscordPage />} />
                  <Route path="updates" element={<UpdatesPage />} />
                </Route>
              </Route>
            </Route>
            <Route path="/watch/movie/:id" element={<WatchPage kind="movie" />} />
            <Route path="/watch/episode/:id" element={<WatchPage kind="episode" />} />
            <Route path="/together/:code" element={<TogetherPage />} />
          </Route>
        </Route>
      </Routes>
    </BootGate>
  );
}
