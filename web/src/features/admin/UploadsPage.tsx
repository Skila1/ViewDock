import { FormEvent, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Upload } from "lucide-react";
import { api } from "@/api/api";
import { formatBytes } from "@/lib/format";
import { VIDEO_ACCEPT } from "@/lib/videoFile";
import { useUploads, type UploadJob } from "@/store/uploads";

function itemHref(job: UploadJob): string | null {
  if (!job.itemId) return null;
  if (job.itemKind === "movie") return `/movies/${job.itemId}`;
  if (job.itemKind === "episode") return `/watch/episode/${job.itemId}`;
  return null;
}

function statusLabel(s: UploadJob["status"]): string {
  switch (s) {
    case "queued":
      return "Queued";
    case "uploading":
      return "Uploading";
    case "paused":
      return "Paused";
    case "processing":
      return "Processing";
    case "complete":
      return "Complete";
    case "failed":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    default:
      return s;
  }
}

export function UploadsPage() {
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const jobs = useUploads((s) => s.jobs);
  const enqueue = useUploads((s) => s.enqueue);
  const hydrate = useUploads((s) => s.hydrate);
  const [libraryId, setLibraryId] = useState("");
  const [over, setOver] = useState(false);
  const [note, setNote] = useState("");

  const enabled = useMemo(() => (libs.data ?? []).filter((l) => l.uploads_enabled), [libs.data]);

  useEffect(() => {
    void hydrate().catch(() => undefined);
  }, [hydrate]);

  useEffect(() => {
    if (!libraryId && enabled[0]) setLibraryId(enabled[0].id);
  }, [enabled, libraryId]);

  const dest = enabled.find((l) => l.id === libraryId);

  const add = (files: FileList | File[] | null) => {
    if (!files?.length || !dest) return;
    const ids = enqueue(files, dest.id, dest.name);
    setNote(ids.length ? "" : "Only video files up to 10 GB are accepted.");
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
  };

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-base font-medium">Uploads</h1>
        <p className="text-sm text-dim">
          Add videos to a library from this browser. Maximum 10 GB per file. Hidden and non-video files are ignored.
          Duplicate names get a number, existing files are never overwritten.
        </p>
      </div>

      <form onSubmit={onSubmit} className="max-w-xl space-y-3">
        <label className="block text-xs text-dim">
          Destination library
          <select className="mt-1 w-full" value={libraryId} onChange={(e) => setLibraryId(e.target.value)}>
            {enabled.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name} ({l.content_type})
              </option>
            ))}
          </select>
        </label>
        <div
          className={`rounded-md border border-dashed px-3 py-8 text-center text-xs ${
            over ? "border-accent bg-overlay" : "border-line"
          }`}
          onDragOver={(e) => {
            e.preventDefault();
            setOver(true);
          }}
          onDragLeave={() => setOver(false)}
          onDrop={(e) => {
            e.preventDefault();
            setOver(false);
            add(e.dataTransfer.files);
          }}
        >
          <Upload size={18} className="mx-auto mb-2 text-dim" />
          <p className="text-dim">Drop video files here, or choose them. You can keep browsing while they upload.</p>
          <input
            className="mx-auto mt-3 block text-xs"
            type="file"
            accept={VIDEO_ACCEPT}
            multiple
            onChange={(e) => {
              add(e.target.files);
              e.target.value = "";
            }}
          />
        </div>
        {note ? <p className="text-xs text-warn">{note}</p> : null}
      </form>

      <section className="space-y-2">
        <h2 className="text-[13px] font-medium text-dim">Active and recent</h2>
        {jobs.length === 0 ? <p className="text-xs text-dim">No uploads yet.</p> : null}
        <ul className="divide-y divide-line rounded-md border border-line">
          {jobs.map((job) => (
            <UploadRow key={job.localId} job={job} />
          ))}
        </ul>
      </section>
    </div>
  );
}

function UploadRow({ job }: { job: UploadJob }) {
  const pause = useUploads((s) => s.pause);
  const resume = useUploads((s) => s.resume);
  const cancel = useUploads((s) => s.cancel);
  const retry = useUploads((s) => s.retry);
  const attachFile = useUploads((s) => s.attachFile);
  const pct = job.size > 0 ? Math.min(100, Math.round((job.offset / job.size) * 100)) : 0;
  const href = itemHref(job);

  return (
    <li className="space-y-2 px-3 py-3 text-sm">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <div>
          <p className="font-medium">{job.filename}</p>
          <p className="text-xs text-dim">
            {formatBytes(job.offset)} of {formatBytes(job.size)}
            {job.libraryName ? ` · ${job.libraryName}` : ""}
            {job.bps ? ` · ${formatBytes(job.bps)}/s` : ""}
            {job.etaSec && job.status === "uploading" ? ` · ${Math.ceil(job.etaSec)}s left` : ""}
          </p>
        </div>
        <span className="text-xs text-dim">{statusLabel(job.status)}</span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-overlay">
        <div className="h-full bg-accent" style={{ width: `${pct}%` }} />
      </div>
      {job.error ? <p className="text-xs text-danger">{job.error}</p> : null}
      <div className="flex flex-wrap gap-2 text-xs">
        {job.status === "uploading" ? (
          <button type="button" className="text-accent" onClick={() => pause(job.localId)}>
            Pause
          </button>
        ) : null}
        {job.status === "paused" || job.status === "failed" ? (
          <button type="button" className="text-accent" onClick={() => retry(job.localId)}>
            {job.file ? "Resume" : "Retry"}
          </button>
        ) : null}
        {job.status === "paused" && !job.file ? (
          <label className="text-accent">
            Reselect file
            <input
              type="file"
              className="sr-only"
              accept={VIDEO_ACCEPT}
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) {
                  attachFile(job.localId, f);
                  resume(job.localId, f);
                }
              }}
            />
          </label>
        ) : null}
        {job.status === "queued" || job.status === "uploading" || job.status === "paused" ? (
          <button type="button" className="text-danger" onClick={() => void cancel(job.localId)}>
            Cancel
          </button>
        ) : null}
        {href && job.status === "complete" ? (
          <Link className="text-accent" to={href}>
            Open
          </Link>
        ) : null}
      </div>
    </li>
  );
}
