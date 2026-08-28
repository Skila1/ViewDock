import { Copy, Radio, Users } from "lucide-react";
import { cn } from "@/lib/cn";
import type { WTPeer, WTSync } from "./useWatchTogether";

type Props = {
  code?: string;
  title?: string;
  peers: WTPeer[];
  sync: WTSync;
  error?: string | null;
  guest?: boolean;
  invitePath?: string;
};

export function WatchTogetherOverlay({
  code,
  title,
  peers,
  sync,
  error,
  guest,
  invitePath,
}: Props) {
  const copy = () => {
    const url = invitePath ? `${window.location.origin}${invitePath}` : code ?? "";
    void navigator.clipboard.writeText(url);
  };

  return (
    <aside className="pointer-events-auto absolute top-14 right-3 z-20 w-64 rounded-lg border border-line bg-overlay/95 p-3 text-sm shadow-lg backdrop-blur">
      <div className="mb-2 flex items-center gap-2 text-ink">
        <Radio size={14} className="text-accent" />
        <span className="font-medium">Watch Together</span>
      </div>
      {title ? <p className="mb-2 truncate text-dim">{title}</p> : null}
      {code ? (
        <div className="mb-2 flex items-center gap-2">
          <code className="flex-1 truncate rounded bg-bg px-2 py-1 text-xs">{code}</code>
          <button type="button" className="rounded border border-line p-1" onClick={copy} aria-label="Copy invite">
            <Copy size={14} />
          </button>
        </div>
      ) : null}
      <div className="mb-2 flex items-center gap-2 text-xs text-dim">
        <span className={cn("h-1.5 w-1.5 rounded-full", sync.playing ? "bg-ok" : "bg-warn")} />
        {sync.playing ? "Playing" : "Paused"}
      </div>
      <div className="flex items-center gap-1 text-xs text-dim">
        <Users size={12} />
        {peers.length ? `${peers.length} watching` : "Waiting for peers"}
      </div>
      {peers.length > 0 ? (
        <ul className="mt-2 space-y-1 text-xs">
          {peers.map((p) => (
            <li key={p.id} className="truncate text-ink">
              {p.name}
              {p.host ? <span className="ml-1 text-dim">host</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
      {error ? <p className="mt-2 text-xs text-danger">{error}</p> : null}
      {guest ? <p className="mt-2 text-[11px] text-dim">Guest session — no library access.</p> : null}
    </aside>
  );
}
