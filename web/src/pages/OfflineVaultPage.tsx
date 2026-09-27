import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { AlertTriangle, CheckCircle2, HardDrive, Pause, Play, RotateCcw, ShieldCheck, Trash2, X } from "lucide-react";
import { getOfflinePolicy, type OfflinePolicy, type PolicySource } from "@/api/vault";
import { useAuth } from "@/store/auth";
import { formatBytes } from "@/lib/offlineVault";
import type { VaultRecord } from "@/lib/vault/db";
import {
  isDownloading,
  isWaiting,
  listDownloads,
  pauseDownload,
  reconcileVault,
  removeDownload,
  requestPersistence,
  resumeDownload,
  serviceWorkerServesVault,
  setVaultPolicy,
  storageEstimate,
  storagePersisted,
  subscribeVault,
  vaultBlob,
  vaultMediaUrl,
  verifyDownload,
  type ReconcileReport,
} from "@/lib/vault/manager";
import { isExpired } from "@/lib/vault/policy";
import { PROGRESS_INTERVAL_MS, recordVaultProgress } from "@/lib/vault/progress";

// Without a controlling service worker a title is decrypted into memory.
const BLOB_FALLBACK_LIMIT = 2 * 1024 ** 3;

function reportText(report: ReconcileReport): string {
  const parts: string[] = [];
  if (report.migrated) parts.push(`${report.migrated} saved title${report.migrated === 1 ? " was" : "s were"} upgraded to encrypted storage`);
  if (report.expired) parts.push(`${report.expired} expired title${report.expired === 1 ? " was" : "s were"} removed`);
  if (report.revoked) parts.push(`${report.revoked} title${report.revoked === 1 ? " is" : "s are"} no longer permitted offline and ${report.revoked === 1 ? "was" : "were"} removed`);
  if (report.evicted) parts.push(`the browser reclaimed storage from ${report.evicted} title${report.evicted === 1 ? "" : "s"}`);
  if (report.unreadable) parts.push(`${report.unreadable} title${report.unreadable === 1 ? " was" : "s were"} unreadable and removed`);
  return parts.length ? `${parts.join("; ")}.` : "";
}

export function OfflineVaultPage() {
  const me = useAuth((s) => s.me);
  const downloadsOff = useAuth((s) => s.system?.features?.downloads === false);
  const userId = me?.id ?? "";
  const [items, setItems] = useState<VaultRecord[]>([]);
  const [policy, setPolicy] = useState<OfflinePolicy | null>(null);
  const [policySource, setPolicySource] = useState<PolicySource>("default");
  const [estimate, setEstimate] = useState<StorageEstimate>({});
  const [persisted, setPersisted] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [unavailable, setUnavailable] = useState(false);
  const [, setTick] = useState(0);

  const refreshStorage = useCallback(async () => {
    setEstimate(await storageEstimate());
    setPersisted(await storagePersisted());
  }, []);

  const refresh = useCallback(async () => {
    if (!userId) return;
    try {
      setItems(await listDownloads(userId));
      setUnavailable(false);
    } catch {
      setUnavailable(true);
    }
    void refreshStorage();
  }, [userId, refreshStorage]);

  useEffect(() => {
    if (!userId) return;
    let cancelled = false;
    void (async () => {
      try {
        const { policy: next, source } = await getOfflinePolicy(userId);
        if (cancelled) return;
        setVaultPolicy(next);
        setPolicy(next);
        setPolicySource(source);
        const report = await reconcileVault(userId, source === "server");
        if (!cancelled) setNotice(reportText(report));
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Offline storage is unavailable in this browser.");
      }
      if (!cancelled) await refresh();
    })();
    return () => {
      cancelled = true;
    };
  }, [userId, refresh]);

  useEffect(
    () =>
      subscribeVault((event) => {
        if (event.type === "update") {
          if (event.record.userId !== userId) return;
          setItems((prev) => {
            const index = prev.findIndex((item) => item.key === event.record.key);
            if (index < 0) return [event.record, ...prev];
            const next = prev.slice();
            next[index] = event.record;
            return next;
          });
          if (event.record.status === "complete" || event.record.status === "quota") void refreshStorage();
        } else if (event.type === "remove") {
          setItems((prev) => prev.filter((item) => item.key !== event.key));
          void refreshStorage();
        } else {
          setTick((n) => n + 1);
          void refresh();
        }
      }),
    [userId, refresh, refreshStorage],
  );

  const offlineAllowed = !downloadsOff && (policy?.enabled ?? true);

  if (!me) {
    return <p className="text-sm text-dim">Sign in to use the Offline Vault.</p>;
  }

  return (
    <section className="space-y-6">
      <header className="flex items-center gap-3">
        <HardDrive className="h-6 w-6 text-accent" />
        <div>
          <h1 className="text-xl font-semibold">Offline Vault</h1>
          <p className="text-sm text-dim">Permitted downloads stay on this device, encrypted, for offline playback.</p>
        </div>
      </header>

      {error ? <Banner tone="danger" onDismiss={() => setError("")}>{error}</Banner> : null}
      {notice ? <Banner tone="info" onDismiss={() => setNotice("")}>{notice}</Banner> : null}
      {unavailable ? <Banner tone="danger">Offline storage is unavailable in this browser. Private browsing modes often disable it.</Banner> : null}

      <StoragePanel
        estimate={estimate}
        persisted={persisted}
        policy={policy}
        policySource={policySource}
        count={items.length}
        onPersist={async () => {
          const ok = await requestPersistence();
          setPersisted(ok);
          if (!ok) setNotice("The browser declined persistent storage. Offline titles may be removed if the device runs low on space.");
        }}
      />

      <div className="space-y-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 className="text-sm font-semibold">
            On this device{items.length ? <span className="ml-2 font-normal text-dim">{items.length}</span> : null}
          </h2>
          {!offlineAllowed ? (
            <p className="text-xs text-dim">New downloads are turned off by the administrator. Saved titles remain playable.</p>
          ) : null}
        </div>
        {items.length ? (
          <div className="grid items-start gap-3 md:grid-cols-2 xl:grid-cols-3">
            {items.map((item) => (
              <VaultCard key={item.key} item={item} userId={userId} canResume={offlineAllowed} onError={setError} />
            ))}
          </div>
        ) : (
          <div className="rounded-lg border border-dashed border-line p-6 text-center text-sm text-dim">
            <p>No offline titles yet.</p>
            {offlineAllowed ? (
              <p className="mt-1">
                Open a movie or episode from{" "}
                <Link to="/" className="text-accent hover:underline">
                  Home
                </Link>{" "}
                and choose <span className="text-ink">Save offline</span>.
              </p>
            ) : null}
          </div>
        )}
      </div>
    </section>
  );
}

function Banner({ tone, children, onDismiss }: { tone: "danger" | "warn" | "info"; children: ReactNode; onDismiss?: () => void }) {
  const styles = {
    danger: "border-danger/40 bg-danger/10 text-danger",
    warn: "border-warn/40 bg-warn/10 text-warn",
    info: "border-line bg-raised text-ink",
  }[tone];
  return (
    <div role={tone === "danger" ? "alert" : "status"} className={`flex items-start justify-between gap-3 rounded-lg border p-3 text-sm ${styles}`}>
      <p>{children}</p>
      {onDismiss ? (
        <button type="button" aria-label="Dismiss" className="shrink-0 text-dim hover:text-ink" onClick={onDismiss}>
          <X className="h-4 w-4" />
        </button>
      ) : null}
    </div>
  );
}

function StoragePanel({ estimate, persisted, policy, policySource, count, onPersist }: {
  estimate: StorageEstimate;
  persisted: boolean;
  policy: OfflinePolicy | null;
  policySource: PolicySource;
  count: number;
  onPersist: () => void;
}) {
  const quota = estimate.quota ?? 0;
  const usage = estimate.usage ?? 0;
  const pct = quota ? Math.min(100, Math.round((usage / quota) * 100)) : 0;
  return (
    <div className="space-y-3 rounded-lg border border-line bg-raised p-3 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="font-medium">Device storage</span>
        <Link to="/device-test" className="text-xs text-accent hover:underline">Check this device</Link>
      </div>
      {quota ? (
        <div className="space-y-1">
          <div className="h-2 overflow-hidden rounded-full bg-overlay" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label="Browser storage used">
            <div className={`h-full ${pct > 85 ? "bg-danger" : "bg-accent"}`} style={{ width: `${pct}%` }} />
          </div>
          <p className="text-xs text-dim">{formatBytes(usage)} used of about {formatBytes(quota)} available to this site.</p>
        </div>
      ) : (
        <p className="text-xs text-dim">This browser does not report its storage quota.</p>
      )}
      {persisted ? (
        <p className="flex items-center gap-2 text-xs text-ok"><ShieldCheck className="h-4 w-4" />Persistent storage granted. The browser should not remove offline titles on its own.</p>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="flex items-center gap-2 text-xs text-warn"><AlertTriangle className="h-4 w-4" />Browser storage is not guaranteed. Offline titles may be removed when the device runs low on space.</p>
          <button type="button" className="rounded-full border border-line px-3 py-1 text-xs" onClick={onPersist}>Ask to keep storage</button>
        </div>
      )}
      {policy ? (
        <p className="text-xs text-dim">
          {count} of {policy.max_items} offline titles used.{" "}
          {policy.expiry_days > 0 ? `Titles expire ${policy.expiry_days} day${policy.expiry_days === 1 ? "" : "s"} after download.` : "Titles do not expire."}
          {policySource === "cached" ? " Using the last policy received from the server." : ""}
          {" "}Signing out removes offline titles from this device.
        </p>
      ) : null}
    </div>
  );
}

function statusText(item: VaultRecord, waiting: boolean): string {
  if (waiting) return "Waiting for another download to finish";
  switch (item.status) {
    case "downloading":
      return isDownloading(item.key) ? "Downloading" : "Interrupted";
    case "verifying":
      return "Verifying integrity";
    case "paused":
      return "Paused";
    case "quota":
      return "Stopped: storage full";
    case "error":
      return "Stopped";
    case "corrupt":
      return "Needs repair";
    case "complete":
      return "Ready offline";
  }
}

function VaultCard({ item, userId, canResume, onError }: { item: VaultRecord; userId: string; canResume: boolean; onError: (message: string) => void }) {
  const [playing, setPlaying] = useState(false);
  const [busy, setBusy] = useState(false);
  const [verified, setVerified] = useState<boolean | null>(null);
  const waiting = isWaiting(item.key);
  const running = isDownloading(item.key) || waiting;
  const complete = item.status === "complete" && Boolean(item.manifest);
  const expired = isExpired(item);
  const pct = item.size ? Math.min(100, Math.floor((item.bytesStored / item.size) * 100)) : 0;

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
    } catch (err) {
      onError(err instanceof Error ? err.message : "That action failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className={`min-w-0 space-y-2 rounded-lg border border-line bg-raised p-3 ${playing ? "md:col-span-2 xl:col-span-3" : ""}`}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-medium">{item.title}</h3>
          <p className="text-xs text-dim">
            {statusText(item, waiting)} · {complete ? formatBytes(item.size) : `${formatBytes(item.bytesStored)} of ${formatBytes(item.size)} (${pct}%)`}
            {item.downloadedAt ? ` · saved ${new Date(item.downloadedAt).toLocaleDateString()}` : ""}
            {item.expiresAt ? ` · ${expired ? "expired" : `expires ${new Date(item.expiresAt).toLocaleDateString()}`}` : ""}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {complete && !expired ? (
            <button type="button" aria-label={`${playing ? "Close" : "Play"} ${item.title}`} className="tap rounded-full p-2 text-dim hover:text-ink" onClick={() => setPlaying((p) => !p)}>
              {playing ? <X className="h-4 w-4" /> : <Play className="h-4 w-4" />}
            </button>
          ) : null}
          {complete ? (
            <button type="button" aria-label={`Verify ${item.title}`} className="tap rounded-full p-2 text-dim hover:text-ink" disabled={busy} onClick={() => void act(async () => setVerified(await verifyDownload(userId, item.key)))}>
              <CheckCircle2 className="h-4 w-4" />
            </button>
          ) : running ? (
            <button type="button" aria-label={`Pause ${item.title}`} className="tap rounded-full p-2 text-dim hover:text-ink" disabled={busy} onClick={() => void act(() => pauseDownload(item.key))}>
              <Pause className="h-4 w-4" />
            </button>
          ) : canResume ? (
            <button type="button" aria-label={`Resume ${item.title}`} className="tap rounded-full p-2 text-dim hover:text-ink" disabled={busy} onClick={() => resumeDownload(userId, item.key)}>
              <RotateCcw className="h-4 w-4" />
            </button>
          ) : null}
          <button type="button" aria-label={`Remove ${item.title}`} className="tap rounded-full p-2 text-dim hover:text-danger" disabled={busy} onClick={() => void act(() => removeDownload(item.key))}>
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </div>
      {!complete ? (
        <div className="h-1.5 overflow-hidden rounded-full bg-overlay" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={`${item.title} download progress`}>
          <div className={`h-full ${item.status === "quota" || item.status === "error" || item.status === "corrupt" ? "bg-danger" : "bg-accent"}`} style={{ width: `${pct}%` }} />
        </div>
      ) : null}
      {verified && complete ? <p className="text-xs text-ok">Integrity verified: every part matches its manifest digest.</p> : null}
      {item.error ? <p className={`text-xs ${item.status === "paused" ? "text-warn" : "text-danger"}`}>{item.error}</p> : null}
      {item.status === "quota" ? <p className="text-xs text-dim">Remove other offline titles or free up disk space on this device, then resume.</p> : null}
      {playing && complete && !expired ? <VaultPlayer item={item} userId={userId} /> : null}
    </article>
  );
}

function VaultPlayer({ item, userId }: { item: VaultRecord; userId: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [src, setSrc] = useState("");
  const [problem, setProblem] = useState("");
  const lastSaved = useRef(0);

  useEffect(() => {
    let revoke = "";
    let cancelled = false;
    void (async () => {
      if (await serviceWorkerServesVault()) {
        if (!cancelled) setSrc(vaultMediaUrl(item.key));
        return;
      }
      if (item.size > BLOB_FALLBACK_LIMIT) {
        if (!cancelled) setProblem("Offline playback of large titles needs the ViewDock service worker. Reload the page once while online, or install the app.");
        return;
      }
      try {
        const blob = await vaultBlob(userId, item.key);
        if (cancelled) return;
        revoke = URL.createObjectURL(blob);
        setSrc(revoke);
      } catch (err) {
        if (!cancelled) setProblem(err instanceof Error ? err.message : "This title could not be opened.");
      }
    })();
    return () => {
      cancelled = true;
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [item.key, item.size, userId]);

  // The video ref is detached before unmount cleanup runs, so keep the position here.
  const position = useRef({ ms: -1, durationMs: 0 });

  const track = () => {
    const video = videoRef.current;
    if (video && Number.isFinite(video.duration) && video.duration > 0) {
      position.current = { ms: video.currentTime * 1000, durationMs: video.duration * 1000 };
    }
  };

  const checkpoint = useCallback((ended = false) => {
    const { ms, durationMs } = position.current;
    if (ms < 0 || durationMs <= 0) return;
    lastSaved.current = Date.now();
    void recordVaultProgress(item, ended ? durationMs : ms, durationMs);
  }, [item]);

  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === "hidden") checkpoint();
    };
    document.addEventListener("visibilitychange", onHide);
    return () => {
      document.removeEventListener("visibilitychange", onHide);
      checkpoint();
    };
  }, [checkpoint]);

  if (problem) return <p className="text-xs text-danger">{problem}</p>;
  if (!src) return <p className="text-xs text-dim">Opening…</p>;
  return (
    <video
      ref={videoRef}
      controls
      autoPlay
      playsInline
      preload="metadata"
      className="w-full rounded-md"
      src={src}
      onLoadedMetadata={(event) => {
        const video = event.currentTarget;
        const resume = (item.positionMs ?? 0) / 1000;
        if (resume > 5 && Number.isFinite(video.duration) && resume < video.duration - 10) video.currentTime = resume;
      }}
      onPause={() => {
        track();
        checkpoint();
      }}
      onSeeked={() => {
        track();
        checkpoint();
      }}
      onEnded={() => {
        track();
        checkpoint(true);
      }}
      onTimeUpdate={() => {
        track();
        if (Date.now() - lastSaved.current >= PROGRESS_INTERVAL_MS && !videoRef.current?.paused) checkpoint();
      }}
      onError={() => setProblem("This browser cannot play this file's format. Check this device's codec support on the device test page.")}
    >
      <track kind="captions" />
    </video>
  );
}
