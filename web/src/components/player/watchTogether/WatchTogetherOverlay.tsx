import { Copy, Radio, Users, X } from "lucide-react";
import { cn } from "@/lib/cn";
import type { PartyMember, PartyPanel, PartySyncInfo } from "./partySync";
import type { PartyStatus, WTSync } from "./useWatchTogether";

type Props = {
  code?: string;
  title?: string;
  members: PartyMember[];
  memberId?: string;
  sync: WTSync;
  status: PartyStatus;
  syncInfo?: PartySyncInfo | null;
  isHost?: boolean;
  sharedControl?: boolean;
  onSharedControl?: (on: boolean) => void;
  panel?: PartyPanel;
  onPanel?: (panel: PartyPanel) => void;
  onClose?: () => void;
  error?: string | null;
  guest?: boolean;
  invitePath?: string;
};

const STATUS_LABEL: Record<PartyStatus, string> = {
  idle: "Not connected",
  connecting: "Connecting",
  synced: "In sync",
  reconnecting: "Not synchronized, reconnecting",
  ended: "Disconnected",
};

function memberState(m: PartyMember, target: number): { label: string; tone: string } {
  if (!m.connected) return { label: "offline", tone: "text-dim" };
  if (m.buffering) return { label: "buffering", tone: "text-warn" };
  if (!m.ready) return { label: "loading", tone: "text-dim" };
  const d = Math.round(m.drift_ms);
  if (Math.abs(d) <= target) return { label: "in sync", tone: "text-ok" };
  return { label: `${d > 0 ? "+" : ""}${(d / 1000).toFixed(1)} s`, tone: Math.abs(d) > 1000 ? "text-danger" : "text-warn" };
}

export function WatchTogetherOverlay({
  code,
  title,
  members,
  memberId,
  sync,
  status,
  syncInfo,
  isHost,
  sharedControl,
  onSharedControl,
  panel = "everyone",
  onPanel,
  onClose,
  error,
  guest,
  invitePath,
}: Props) {
  const copy = () => {
    const url = invitePath ? `${window.location.origin}${invitePath}` : code ?? "";
    void navigator.clipboard.writeText(url);
  };
  const target = syncInfo?.target_ms ?? 250;
  const synced = status === "synced";

  return (
    <aside className="pointer-events-auto absolute top-16 right-3 z-20 max-w-[calc(100vw-1.5rem)] w-64 rounded-lg border border-line bg-overlay/95 p-3 text-sm shadow-lg backdrop-blur">
      <div className="mb-2 flex items-center gap-2 text-ink">
        <Radio size={14} className="text-accent" />
        <span className="flex-1 font-medium">Watch Together</span>
        {onClose ? (
          <button type="button" className="rounded p-0.5 text-dim hover:text-ink" onClick={onClose} aria-label="Hide Watch Together panel">
            <X size={14} />
          </button>
        ) : null}
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
      <div className="mb-1 flex items-center gap-2 text-xs" role="status">
        <span className={cn("h-1.5 w-1.5 rounded-full", synced ? "bg-ok" : "bg-warn")} />
        <span className={synced ? "text-dim" : "text-warn"}>{STATUS_LABEL[status]}</span>
        {synced ? <span className="text-dim">· {sync.playing ? "Playing" : "Paused"}</span> : null}
      </div>
      {synced && syncInfo ? (
        <p className="mb-2 text-[11px] text-dim">
          Worst drift {(syncInfo.max_drift_ms / 1000).toFixed(2)} s across {syncInfo.eligible} active
          {syncInfo.stats.rate_corrections || syncInfo.stats.seeks
            ? ` · ${syncInfo.stats.rate_corrections} gentle, ${syncInfo.stats.seeks} jump corrections`
            : ""}
        </p>
      ) : null}
      <div className="flex items-center gap-1 text-xs text-dim">
        <Users size={12} />
        {members.length ? `${members.length} in party` : "Waiting for others"}
      </div>
      {members.length > 0 ? (
        <ul className="mt-2 space-y-1 text-xs">
          {members.map((m) => {
            const st = memberState(m, target);
            return (
              <li key={m.id} className="flex items-center justify-between gap-2">
                <span className="truncate text-ink">
                  {m.display_name}
                  {m.id === memberId ? <span className="ml-1 text-dim">(you)</span> : null}
                  {m.host ? <span className="ml-1 text-dim">host</span> : null}
                </span>
                <span className={cn("shrink-0", st.tone)}>{st.label}</span>
              </li>
            );
          })}
        </ul>
      ) : null}
      {isHost && onSharedControl ? (
        <label className="mt-2 flex items-center gap-2 text-xs text-dim">
          <input type="checkbox" checked={Boolean(sharedControl)} onChange={(e) => onSharedControl(e.target.checked)} />
          Everyone can pause and seek
        </label>
      ) : null}
      {isHost && onPanel ? (
        <label className="mt-2 flex items-center justify-between gap-2 text-xs text-dim">
          Show this panel to
          <select
            className="rounded border border-line bg-bg px-1 py-0.5 text-xs text-ink"
            value={panel}
            onChange={(e) => onPanel(e.target.value as PartyPanel)}
          >
            <option value="everyone">Everyone</option>
            <option value="host">Only me</option>
            <option value="hidden">Nobody</option>
          </select>
        </label>
      ) : null}
      {isHost && panel === "hidden" ? (
        <p className="mt-1 text-[11px] text-dim">Hidden for everyone. Reopen it from the party button in the player controls.</p>
      ) : null}
      {!isHost && !sharedControl && synced ? (
        <p className="mt-2 text-[11px] text-dim">The host controls playback.</p>
      ) : null}
      {error ? <p className="mt-2 text-xs text-danger">{error}</p> : null}
      {guest ? <p className="mt-2 text-[11px] text-dim">Guest session: no library access.</p> : null}
    </aside>
  );
}
