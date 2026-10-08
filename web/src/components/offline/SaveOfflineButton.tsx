import { useEffect, useState } from "react";
import { Link } from "react-router";
import * as Dialog from "@radix-ui/react-dialog";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Download, Loader2 } from "lucide-react";
import { getOfflinePolicy } from "@/api/vault";
import { cn } from "@/lib/cn";
import { formatBytes } from "@/lib/offlineVault";
import type { VaultRecord } from "@/lib/vault/db";
import {
  discardPrepared,
  listDownloads,
  prepareDownload,
  requestPersistence,
  setVaultPolicy,
  startDownload,
  subscribeVault,
  type PreparedDownload,
  type VaultTitle,
} from "@/lib/vault/manager";
import { libraryAllowed } from "@/lib/vault/policy";
import { useAuth } from "@/store/auth";

/** Offline policy and this device's vault records, shared by every save button on a page. */
function useVaultState(userId: string, enabled: boolean) {
  const qc = useQueryClient();
  const policy = useQuery({
    queryKey: ["offline-policy", userId],
    queryFn: async () => {
      const { policy: next } = await getOfflinePolicy(userId);
      setVaultPolicy(next);
      return next;
    },
    enabled: enabled && Boolean(userId),
    staleTime: 5 * 60_000,
  });
  const records = useQuery({
    queryKey: ["vault-records", userId],
    queryFn: () => listDownloads(userId),
    enabled: enabled && Boolean(userId),
  });

  useEffect(() => {
    if (!enabled || !userId) return;
    const key = ["vault-records", userId];
    return subscribeVault((event) => {
      if (event.type === "update") {
        if (event.record.userId !== userId) return;
        qc.setQueryData<VaultRecord[]>(key, (prev = []) => {
          const index = prev.findIndex((r) => r.key === event.record.key);
          if (index < 0) return [event.record, ...prev];
          const next = prev.slice();
          next[index] = event.record;
          return next;
        });
      } else if (event.type === "remove") {
        qc.setQueryData<VaultRecord[]>(key, (prev = []) => prev.filter((r) => r.key !== event.key));
      } else {
        void qc.invalidateQueries({ queryKey: key });
      }
    });
  }, [enabled, userId, qc]);

  return { policy: policy.data, records: records.data ?? [] };
}

function ConsentDialog({ prepared, onConfirm, onCancel }: { prepared: PreparedDownload | null; onConfirm: () => void; onCancel: () => void }) {
  const quota = prepared?.estimate.quota;
  const free = prepared && quota ? Math.max(0, quota - (prepared.estimate.usage ?? 0)) : undefined;
  return (
    <Dialog.Root open={Boolean(prepared)} onOpenChange={(open) => !open && onCancel()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[min(460px,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 space-y-3 rounded-xl border border-line bg-raised p-4 text-sm shadow-xl">
          {prepared ? (
            <>
              <Dialog.Title className="font-semibold">Save “{prepared.title.title}” to this device?</Dialog.Title>
              <Dialog.Description asChild>
                <ul className="list-disc space-y-1 pl-5 text-dim">
                  <li>
                    Download size: <span className="text-ink">{formatBytes(prepared.size)}</span>
                    {free !== undefined ? <> of about {formatBytes(free)} free for this site</> : null}.
                  </li>
                  <li>Storage location: this browser's private storage for this site (IndexedDB), encrypted with a key kept on this device.</li>
                  <li>
                    {prepared.persisted
                      ? "Persistent storage is granted."
                      : "The browser may remove offline titles when space runs low; you will be asked to allow persistent storage."}
                  </li>
                  {!prepared.ranges ? <li>The server does not support resumable downloads, so pausing restarts from the beginning.</li> : null}
                </ul>
              </Dialog.Description>
              <p className="text-dim">Manage, pause or remove it at any time from Offline.</p>
              <div className="flex flex-wrap justify-end gap-2">
                <button type="button" className="tap rounded-full border border-line px-4 text-xs" onClick={onCancel}>
                  Cancel
                </button>
                <button type="button" className="btn-green tap inline-flex items-center gap-2 rounded-full px-4 text-xs" onClick={onConfirm}>
                  <Download className="h-4 w-4" />
                  Use device storage and download
                </button>
              </div>
            </>
          ) : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

type Props = VaultTitle & {
  /** Icon-only button for dense rows such as episode lists. */
  compact?: boolean;
};

/**
 * Saves a title to the encrypted Offline Vault after the storage consent
 * prompt. Renders nothing when downloads are off or the library is excluded.
 */
export function SaveOfflineButton({ compact, ...title }: Props) {
  const me = useAuth((s) => s.me);
  const downloadsOff = useAuth((s) => s.system?.features?.downloads === false);
  const userId = me?.id ?? "";
  const { policy, records } = useVaultState(userId, !downloadsOff);
  const [prepared, setPrepared] = useState<PreparedDownload | null>(null);
  const [preparing, setPreparing] = useState(false);
  const [error, setError] = useState("");

  if (downloadsOff || !userId || !policy || !libraryAllowed(policy, title.libraryId)) return null;

  const existing = records.find((r) => r.itemKind === title.kind && r.itemId === title.id);
  const base = compact
    ? "tap flex w-10 shrink-0 items-center justify-center rounded-md text-dim hover:text-ink"
    : "tap inline-flex items-center gap-1 rounded-md border border-line px-3 text-sm";

  if (existing) {
    const done = existing.status === "complete";
    const pct = existing.size ? Math.min(100, Math.floor((existing.bytesStored / existing.size) * 100)) : 0;
    const label = done ? "Saved offline" : `Saving ${pct}%`;
    return (
      <Link to="/offline" className={cn(base, done && "text-ok hover:text-ok")} aria-label={`${label}: ${title.title}`} title={label}>
        {done ? <CheckCircle2 size={14} /> : <Loader2 size={14} className="animate-spin" />}
        {compact ? null : label}
      </Link>
    );
  }

  const begin = async () => {
    setError("");
    setPreparing(true);
    try {
      setPrepared(await prepareDownload(userId, title, records.length));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Offline download failed.");
    } finally {
      setPreparing(false);
    }
  };

  const confirm = async () => {
    if (!prepared) return;
    const next = prepared;
    setPrepared(null);
    await requestPersistence();
    try {
      await startDownload(next);
    } catch (err) {
      discardPrepared(next);
      setError(err instanceof Error ? err.message : "Offline download failed.");
    }
  };

  const cancel = () => {
    if (prepared) discardPrepared(prepared);
    setPrepared(null);
  };

  return (
    <>
      <button
        type="button"
        className={cn(base, compact && error && "text-danger hover:text-danger")}
        disabled={preparing}
        aria-label={`Save ${title.title} offline`}
        title={error || "Save offline"}
        onClick={() => void begin()}
      >
        {preparing ? (
          <Loader2 size={14} className="animate-spin" />
        ) : compact && error ? (
          <AlertTriangle size={14} />
        ) : (
          <Download size={14} />
        )}
        {compact ? null : preparing ? "Checking…" : "Save offline"}
      </button>
      {error && !compact ? (
        <p role="alert" className="basis-full text-xs text-danger">
          {error}
        </p>
      ) : null}
      {error && compact ? <span className="sr-only" role="alert">{error}</span> : null}
      <ConsentDialog prepared={prepared} onConfirm={() => void confirm()} onCancel={cancel} />
    </>
  );
}
