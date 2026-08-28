import { Outlet } from "react-router";

export function GuestShell() {
  return (
    <div className="min-h-dvh bg-black text-ink">
      <Outlet />
    </div>
  );
}
