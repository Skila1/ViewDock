import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ChevronUp, LogOut, Settings } from "lucide-react";
import { api } from "@/api/api";
import { cn } from "@/lib/cn";
import { useAuth } from "@/store/auth";

function discordAvatar(id: string, hash?: string) {
  if (!hash || !/^[a-z0-9_]+$/i.test(hash) || !/^\d+$/.test(id)) return "";
  return `https://cdn.discordapp.com/avatars/${id}/${hash}.${hash.startsWith("a_") ? "gif" : "png"}?size=64`;
}

function Avatar({ src, name, className }: { src: string; name: string; className?: string }) {
  const [failed, setFailed] = useState(false);
  const initial = (name.trim()[0] ?? "?").toUpperCase();
  if (src && !failed) {
    return <img src={src} alt="" onError={() => setFailed(true)} className={cn("shrink-0 rounded-full object-cover", className)} />;
  }
  return (
    <span className={cn("flex shrink-0 items-center justify-center rounded-full bg-accent/20 text-xs font-semibold text-accent", className)}>
      {initial}
    </span>
  );
}

/** Account button at the foot of the sidebar; opens upwards with settings and sign out. */
export function ProfileMenu({ collapsed, linkClass }: { collapsed: boolean; linkClass: string }) {
  const { me, logout } = useAuth();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const identities = useQuery({ queryKey: ["identities"], queryFn: api.listIdentities, enabled: Boolean(me), staleTime: 5 * 60_000 });
  const discord = identities.data?.find((i) => i.provider === "discord");
  const avatar = discord ? discordAvatar(discord.provider_user_id, discord.avatar_hash) : "";
  const name = me?.display_name || me?.username || "Profile";

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);

  return (
    <div ref={root} className="relative border-t border-line p-2">
      {open ? (
        <div className={cn("absolute bottom-full z-50 mb-1 rounded-lg border border-line bg-raised p-1 shadow-xl", collapsed ? "left-2 w-56" : "inset-x-2")}>
          <div className="flex items-center gap-3 px-3 py-2">
            <Avatar src={avatar} name={name} className="h-9 w-9" />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-ink">{name}</p>
              <p className="truncate text-xs text-dim">@{me?.username}</p>
              {discord ? <p className="truncate text-[11px] text-dim">Discord: {discord.provider_username}</p> : null}
            </div>
          </div>
          <Link to="/profile" className={linkClass} onClick={() => setOpen(false)}>
            <Settings className="h-4 w-4 shrink-0" />
            Settings
          </Link>
          <button
            type="button"
            className={cn(linkClass, "w-full")}
            onClick={async () => {
              setOpen(false);
              await logout();
              navigate("/login");
            }}
          >
            <LogOut className="h-4 w-4 shrink-0" />
            Sign out
          </button>
        </div>
      ) : null}
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        title={collapsed ? name : undefined}
        onClick={() => setOpen((v) => !v)}
        className={cn(linkClass, "w-full", open && "bg-overlay text-ink", collapsed && "justify-center px-0")}
      >
        <Avatar src={avatar} name={name} className="h-6 w-6" />
        {!collapsed ? (
          <>
            <span className="min-w-0 flex-1 truncate text-left">{name}</span>
            <ChevronUp className={cn("h-4 w-4 shrink-0 transition-transform", !open && "rotate-180")} />
          </>
        ) : null}
      </button>
    </div>
  );
}
