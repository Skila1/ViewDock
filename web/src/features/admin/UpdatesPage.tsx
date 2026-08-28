import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { UpdateStatus } from "@/types/api.gen";

function relativeTime(raw?: string | null): string {
  if (!raw) return "never";
  const t = new Date(raw).getTime();
  if (Number.isNaN(t)) return raw;
  const s = Math.round((Date.now() - t) / 1000);
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

function Badge({ tone, children }: { tone?: "ok" | "warn" | "accent"; children: string }) {
  const cls =
    tone === "ok"
      ? "border-ok/40 text-ok"
      : tone === "warn"
        ? "border-warn/40 text-warn"
        : tone === "accent"
          ? "border-accent/40 text-accent"
          : "border-line text-dim";
  return <span className={`rounded-full border px-2 py-0.5 text-xs ${cls}`}>{children}</span>;
}

export function UpdatesPage() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [watch, setWatch] = useState(false);
  const [msg, setMsg] = useState("");
  const q = useQuery({
    queryKey: ["admin-updates"],
    queryFn: api.getUpdates,
    refetchInterval: (query) => {
      const cur = query.state.data as UpdateStatus | undefined;
      if (watch || cur?.updating || cur?.last_status === "updating") return 1000;
      return 8000;
    },
  });
  const d = q.data || ({} as UpdateStatus);
  const updating = !!(
    d.updating ||
    d.last_status === "updating" ||
    d.progress?.stage === "pulling" ||
    d.progress?.stage === "restarting" ||
    d.progress?.stage === "queued"
  );
  const changelog = Array.isArray(d.changelog) ? d.changelog : [];
  const pct = typeof d.progress?.percent === "number" ? d.progress.percent : updating ? 12 : 0;

  useEffect(() => {
    if (!watch) return;
    if (updating) return;
    if (d.last_status === "error") {
      setWatch(false);
      setMsg(d.last_error || "Update failed");
      return;
    }
    if (d.last_status === "ok") {
      setWatch(false);
      setMsg(`Updated to ${d.version || "the latest image"}`);
      const t = setTimeout(() => location.reload(), 800);
      return () => clearTimeout(t);
    }
  }, [watch, updating, d.last_status, d.last_error, d.version]);

  return (
    <div className="max-w-2xl space-y-4">
      <div>
        <h1 className="text-base font-medium">Updates</h1>
        <p className="text-sm text-dim">
          Check now talks to GitHub and works on any host. Update now only works when the installer helper
          or a Docker socket can recreate this container. SQLite stays on the config volume.
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <Badge tone={d.available ? "warn" : "ok"}>{d.available ? "Update available" : "Up to date"}</Badge>
        {updating ? (
          <Badge tone="accent">Updating</Badge>
        ) : d.can_apply ? (
          <Badge tone="ok">Can install</Badge>
        ) : (
          <Badge tone="warn">Cannot install here</Badge>
        )}
        {d.helper_ok ? <Badge tone="ok">Host helper</Badge> : <Badge>No host helper</Badge>}
        {d.socket_ok ? <Badge tone="ok">Docker socket</Badge> : <Badge>No Docker socket</Badge>}
        <Badge>{d.last_status || "idle"}</Badge>
      </div>
      <div className="rounded-xl border border-line bg-raised p-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <div className="text-sm text-dim">Installed</div>
            <div className="text-2xl font-semibold">{d.version || "0.1.0"}</div>
          </div>
          <div>
            <div className="text-sm text-dim">Latest</div>
            <div className="text-2xl font-semibold">{d.latest_version || d.version || "0.1.0"}</div>
          </div>
        </div>
        <p className="mt-1 break-all text-xs text-dim">{d.image}</p>
        <p className="mt-3 text-sm text-dim">Last check: {relativeTime(d.last_check_at)}</p>
        <p className="text-sm text-dim">
          Last update: {relativeTime(d.last_applied_at)}
          {d.last_applied_by ? ` (${d.last_applied_by})` : ""}
        </p>
        {d.last_error ? <p className="mt-2 text-sm text-danger">{d.last_error}</p> : null}
        {d.apply_reason ? <p className="mt-2 text-sm text-dim">{d.apply_reason}</p> : null}

        {d.available && !updating ? (
          <div className="mt-4 rounded-lg border border-line bg-overlay p-4">
            <div className="text-sm font-medium">
              {d.latest_version && d.latest_version !== d.version ? `Version ${d.latest_version}` : "Newer image"}
            </div>
            {changelog.length ? (
              <ul className="mt-3 space-y-3">
                {changelog.map((rel) => (
                  <li key={rel.version}>
                    <div className="text-xs font-semibold text-dim">{rel.version}</div>
                    <ul className="mt-1 list-disc space-y-1 pl-5 text-sm">
                      {(rel.notes || []).map((n, i) => (
                        <li key={i}>{n}</li>
                      ))}
                    </ul>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-2 text-sm text-dim">A newer image is ready. Changelog will show after Check now can reach GitHub.</p>
            )}
          </div>
        ) : null}

        {updating ? (
          <div className="mt-4 space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="font-medium">
                {d.progress?.stage === "restarting"
                  ? "Starting new containers"
                  : d.progress?.stage === "queued"
                    ? "Waiting for the host"
                    : "Pulling image"}
              </span>
              <span className="text-dim">{pct}%</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-overlay">
              <div className="h-full bg-accent transition-all" style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
            </div>
            <p className="text-xs text-dim">{d.progress?.detail || "The host is pulling the latest image."}</p>
            {d.progress?.log ? (
              <pre className="max-h-36 overflow-auto rounded-md bg-overlay p-3 font-mono text-[11px] leading-4 text-dim">
                {d.progress.log}
              </pre>
            ) : null}
          </div>
        ) : null}

        {msg ? <p className="mt-3 text-sm text-dim">{msg}</p> : null}

        <div className="mt-4 flex flex-wrap gap-2">
          <button
            type="button"
            className="rounded-md border border-line px-3 py-1.5 text-sm"
            disabled={busy || d.checking || updating}
            onClick={() => {
              void (async () => {
                setBusy(true);
                setMsg("");
                try {
                  await api.checkUpdates();
                  await qc.invalidateQueries({ queryKey: ["admin-updates"] });
                  setMsg("Checked for updates");
                } catch {
                  setMsg("Could not check for updates");
                } finally {
                  setBusy(false);
                }
              })();
            }}
          >
            Check now
          </button>
          <button
            type="button"
            className="btn-green rounded-md px-3 py-1.5 text-sm disabled:opacity-50"
            disabled={busy || updating || !d.can_apply || !d.available}
            onClick={() => {
              void (async () => {
                setBusy(true);
                setMsg("");
                try {
                  await api.applyUpdates();
                  setWatch(true);
                  setMsg("Host is pulling the new image");
                  await qc.invalidateQueries({ queryKey: ["admin-updates"] });
                } catch (e) {
                  setMsg(e instanceof Error ? e.message : "Could not start update");
                } finally {
                  setBusy(false);
                }
              })();
            }}
          >
            {updating ? "Updating…" : "Update now"}
          </button>
        </div>
      </div>
      <label className="flex max-w-lg items-center justify-between rounded-xl border border-line bg-raised p-4">
        <span>
          <span className="block text-sm font-medium">Automatic updates</span>
          <span className="block text-xs text-dim">When on, ViewDock checks about once an hour and pulls a newer image on the host.</span>
        </span>
        <input
          type="checkbox"
          checked={!!d.auto_enabled}
          onChange={(e) => {
            const on = e.target.checked;
            void api.putUpdates({ auto_enabled: on }).then(() => {
              setMsg(on ? "Automatic updates on" : "Automatic updates off");
              void qc.invalidateQueries({ queryKey: ["admin-updates"] });
            });
          }}
        />
      </label>
    </div>
  );
}
