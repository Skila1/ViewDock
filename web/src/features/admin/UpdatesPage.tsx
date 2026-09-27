import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { UpdateStatus } from "@/types/api.gen";
import { Card, CardGrid, PageHeader, Pill, primaryBtn, secondaryBtn } from "./ui";

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

function Check({ ok, label }: { ok: boolean; label: string }) {
  return (
    <li className="flex items-center justify-between gap-2">
      <span>{label}</span>
      <Pill tone={ok ? "ok" : "dim"}>{ok ? "Available" : "Not found"}</Pill>
    </li>
  );
}

export function UpdatesPage() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [watch, setWatch] = useState(false);
  const [msg, setMsg] = useState("");
  const runRef = useRef<{ version: string; seen: boolean } | null>(null);
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
  const available = Boolean(
    d.available && d.latest_version && d.version && d.latest_version !== d.version,
  );
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
    const run = runRef.current;
    if (!watch || !run) return;
    if (updating) {
      run.seen = true;
      return;
    }
    // Status loaded before the host picked up the request still describes the previous run.
    if (!run.seen && d.version === run.version) return;
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

  const checkNow = async () => {
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
  };
  const updateNow = async () => {
    setBusy(true);
    setMsg("");
    try {
      runRef.current = { version: d.version, seen: false };
      await api.applyUpdates();
      setWatch(true);
      setMsg("Host is pulling the new image");
      await qc.invalidateQueries({ queryKey: ["admin-updates"] });
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Could not start update");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader
        title="Updates"
        description="Check now talks to GitHub and works on any host. Update now needs the installer helper or a Docker socket to recreate this container. SQLite stays on the config volume."
        actions={
          <>
            <button type="button" className={secondaryBtn} disabled={busy || d.checking || updating} onClick={() => void checkNow()}>
              Check now
            </button>
            <button
              type="button"
              className={`${primaryBtn} disabled:opacity-50`}
              disabled={busy || updating || !d.can_apply || !available}
              onClick={() => void updateNow()}
            >
              {updating ? "Updating…" : "Update now"}
            </button>
          </>
        }
      />
      {msg ? (
        <p role="status" className="text-sm text-dim">
          {msg}
        </p>
      ) : null}
      <CardGrid>
        <Card
          id="updates-version"
          title="Version"
          aside={
            <Pill tone={updating ? "accent" : available ? "warn" : "ok"}>
              {updating ? "Updating" : available ? "Update available" : "Up to date"}
            </Pill>
          }
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <div className="text-xs text-dim">Installed</div>
              <div className="text-2xl font-semibold">{d.version || "0.1.0"}</div>
            </div>
            <div>
              <div className="text-xs text-dim">Latest</div>
              <div className="text-2xl font-semibold">{d.latest_version || d.version || "0.1.0"}</div>
            </div>
          </div>
          <p className="break-all text-xs text-dim">{d.image}</p>
          <p className="text-xs text-dim">
            Last check: {relativeTime(d.last_check_at)}. Last update: {relativeTime(d.last_applied_at)}
            {d.last_applied_by ? ` (${d.last_applied_by})` : ""}.
          </p>
          {d.last_error ? <p className="text-sm text-danger">{d.last_error}</p> : null}

          {updating ? (
            <div className="space-y-2 border-t border-line pt-3">
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
        </Card>

        <Card
          id="updates-install"
          title="Installing"
          aside={
            updating ? null : <Pill tone={d.can_apply ? "ok" : "warn"}>{d.can_apply ? "Can install" : "Cannot install here"}</Pill>
          }
        >
          <ul className="space-y-1 text-sm">
            <Check ok={!!d.helper_ok} label="Host helper" />
            <Check ok={!!d.socket_ok} label="Docker socket" />
          </ul>
          {d.apply_reason ? <p className="text-xs text-dim">{d.apply_reason}</p> : null}
          <label className="flex items-center justify-between gap-3 border-t border-line pt-3">
            <span>
              <span className="block text-sm font-medium">Automatic updates</span>
              <span className="block text-xs text-dim">When on, ViewDock checks about once an hour and pulls a newer image on the host.</span>
            </span>
            <input
              type="checkbox"
              checked={!!d.auto_enabled}
              onChange={(e) => {
                const on = e.target.checked;
                void api
                  .putUpdates({ auto_enabled: on })
                  .then(() => {
                    setMsg(on ? "Automatic updates on" : "Automatic updates off");
                    void qc.invalidateQueries({ queryKey: ["admin-updates"] });
                  })
                  .catch(() => setMsg("Could not change automatic updates"));
              }}
            />
          </label>
        </Card>

        {available && !updating ? (
          <Card id="updates-changelog" title={`What's new in ${d.latest_version}`} className="lg:col-span-2">
            {changelog.length ? (
              <ul className="space-y-3">
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
              <p className="text-sm text-dim">A newer image is ready. The changelog shows once Check now can reach GitHub.</p>
            )}
          </Card>
        ) : null}
      </CardGrid>
    </div>
  );
}
