import { useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { backupsApi, type BackupSummary, type BackupValidation } from "@/api/backups";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/cn";

const QUERY_KEY = ["admin-backups"];

const TRIGGER_LABEL: Record<string, string> = {
  manual: "Manual",
  scheduled: "Scheduled",
  "pre-restore": "Before restore",
  cli: "Command line",
};

function errText(e: unknown, fallback: string) {
  return e instanceof Error && e.message ? e.message : fallback;
}

function when(iso?: string) {
  return iso ? new Date(iso).toLocaleString() : "";
}

function ValidationResult({ v }: { v: BackupValidation }) {
  return (
    <div className={cn("space-y-1 rounded-md border px-3 py-2 text-xs", v.valid ? "border-line" : "border-danger")}>
      <p className={v.valid ? "text-ok" : "text-danger"}>
        {v.valid ? "Backup verified" : "Backup failed verification"} · {v.files_checked} file{v.files_checked === 1 ? "" : "s"} ·{" "}
        {formatBytes(v.bytes)}
      </p>
      <p className="text-dim">
        Schema {v.schema_version} (this database: {v.current_schema}) ·{" "}
        {v.restore_mode === "file" ? "restores by replacing the database file" : "restores by copying rows"} ·{" "}
        {v.current_database_empty ? "current database is empty" : "current database has data, so a restore needs --force"}
      </p>
      {v.master_key_matches === false ? (
        <p className="text-warn">
          This backup was made with a different master key. Encrypted settings in it can only be read with that key.
        </p>
      ) : null}
      {v.problems.map((p) => (
        <p key={p} className="text-danger">
          {p}
        </p>
      ))}
    </div>
  );
}

function BackupRow({ b }: { b: BackupSummary }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [validation, setValidation] = useState<BackupValidation | null>(null);

  const validate = async () => {
    setErr("");
    setBusy(true);
    try {
      setValidation(await backupsApi.validate(b.id));
    } catch (e) {
      setErr(errText(e, "the backup could not be verified"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!window.confirm(`Delete backup ${b.id}? This cannot be undone.`)) return;
    setErr("");
    setBusy(true);
    try {
      await backupsApi.remove(b.id);
    } catch (e) {
      setErr(errText(e, "the backup could not be deleted"));
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
    }
  };

  return (
    <li className="space-y-2 px-3 py-3 text-sm">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="font-medium">{when(b.created_at)}</p>
          <p className="break-all text-xs text-dim">
            {b.id} · {TRIGGER_LABEL[b.trigger] ?? b.trigger} · {b.kind === "sqlite-snapshot" ? "SQLite snapshot" : "JSON export"} ·{" "}
            {formatBytes(b.size)}
            {b.tables ? ` · ${b.tables} tables, ${b.rows.toLocaleString()} rows` : ""}
          </p>
          <p className="text-xs text-dim">
            ViewDock {b.app_version} · {b.dialect} · schema {b.schema_version}
            {b.master_key_id ? ` · master key ${b.master_key_id}` : ""}
          </p>
        </div>
        <div className="flex flex-wrap gap-3 text-xs">
          <button type="button" disabled={busy} onClick={() => void validate()}>
            Verify
          </button>
          <a href={backupsApi.downloadUrl(b.id)} download={`${b.id}.tar.gz`}>
            Download
          </a>
          <button type="button" className="text-danger" disabled={busy} onClick={() => void remove()}>
            Delete
          </button>
        </div>
      </div>
      {validation ? <ValidationResult v={validation} /> : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </li>
  );
}

export function BackupsPage() {
  const qc = useQueryClient();
  const backups = useQuery({
    queryKey: QUERY_KEY,
    queryFn: backupsApi.list,
    refetchInterval: (q) => (q.state.data?.status.running ? 5_000 : false),
  });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [check, setCheck] = useState<{ ok: boolean; message: string } | null>(null);
  const status = backups.data?.status;
  const items = backups.data?.items ?? [];

  const create = async () => {
    setErr("");
    setBusy(true);
    try {
      await backupsApi.create();
    } catch (e) {
      setErr(errText(e, "the backup could not be created"));
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: QUERY_KEY });
    }
  };

  const testDestination = async () => {
    setCheck(null);
    try {
      setCheck(await backupsApi.checkDestination());
    } catch (e) {
      setCheck({ ok: false, message: errText(e, "the destination could not be checked") });
    }
  };

  return (
    <div className="max-w-3xl space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-base font-medium">Backups</h1>
          <p className="text-sm text-dim">
            Consistent backups of accounts, libraries, history and configuration. Media files are not included; back up
            your media folders and object storage separately.
          </p>
        </div>
        <button
          type="button"
          className="btn-green shrink-0 rounded-full px-4 py-1.5 text-sm"
          disabled={busy || status?.running}
          onClick={() => void create()}
        >
          {busy || status?.running ? "Backing up…" : "Back up now"}
        </button>
      </div>

      {status ? (
        <div className="space-y-1 rounded-md border border-line px-3 py-3 text-sm">
          <p>
            Destination: <span className="text-ink">{status.destination === "s3" ? "S3-compatible storage" : "Local folder"}</span>
            {status.location ? <span className="break-all text-dim"> · {status.location}</span> : null}
          </p>
          <p className="text-dim">
            {status.schedule_hours > 0 ? `Automatic backup every ${status.schedule_hours} h` : "Automatic backups are off"} · keeps the
            newest {status.retention}
            {status.next_run ? ` · next ${when(status.next_run)}` : ""}
          </p>
          {status.last_failure_at ? (
            <p className="text-danger">The last scheduled backup failed at {when(status.last_failure_at)}. See Logs for details.</p>
          ) : null}
          <div className="flex flex-wrap items-center gap-3 pt-1 text-xs">
            <Link to="/admin/settings">Change backup settings</Link>
            <button type="button" onClick={() => void testDestination()}>
              Test destination
            </button>
            {check ? <span className={check.ok ? "text-ok" : "text-danger"}>{check.message}</span> : null}
          </div>
        </div>
      ) : null}

      {status?.notice ? (
        <p className="rounded-md border border-line bg-raised px-3 py-2 text-xs text-dim">
          {status.notice}
          {status.master_key_id ? (
            <>
              {" "}
              Current master key fingerprint: <code className="text-ink">{status.master_key_id}</code>.
            </>
          ) : null}
        </p>
      ) : null}

      {err ? <p className="text-sm text-danger">{err}</p> : null}
      {backups.data?.destination_error ? <p className="text-sm text-danger">{backups.data.destination_error}</p> : null}
      {backups.isLoading ? <p className="text-sm text-dim">Loading backups…</p> : null}
      {backups.isError ? <p className="text-sm text-danger">{errText(backups.error, "backups could not be loaded")}</p> : null}
      {backups.isSuccess && !backups.data.destination_error && items.length === 0 ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">No backups yet.</p>
      ) : null}
      {items.length ? (
        <ul className="divide-y divide-line rounded-md border border-line">
          {items.map((b) => (
            <BackupRow key={b.id} b={b} />
          ))}
        </ul>
      ) : null}

      <p className="text-xs text-dim">
        Restoring replaces the database, so it runs from the command line with the server stopped:{" "}
        <code className="text-ink">viewdock backup restore &lt;id&gt;</code>. Use Verify first to check the files and schema.
      </p>
    </div>
  );
}
