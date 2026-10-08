import { create } from "zustand";
import { api, ApiError } from "@/api/api";
import type { UploadSession } from "@/types/api.gen";
import { MAX_UPLOAD_BYTES, UPLOAD_CHUNK, pickUploadFiles } from "@/lib/videoFile";

export type UploadState = "queued" | "uploading" | "paused" | "processing" | "complete" | "failed" | "cancelled";

export type UploadJob = {
  localId: string;
  id?: string;
  file?: File;
  filename: string;
  size: number;
  offset: number;
  libraryId: string;
  libraryName: string;
  status: UploadState;
  error?: string;
  bps?: number;
  etaSec?: number;
  itemKind?: string;
  itemId?: string;
};

type UploadsStore = {
  jobs: UploadJob[];
  enqueue: (files: FileList | File[], libraryId: string, libraryName: string) => string[];
  pause: (localId: string) => void;
  resume: (localId: string, file?: File) => void;
  cancel: (localId: string) => Promise<void>;
  retry: (localId: string) => void;
  hydrate: () => Promise<void>;
  attachFile: (localId: string, file: File) => void;
};

const controllers = new Map<string, AbortController>();
const paused = new Set<string>();
let pumping = false;

function mapServer(s: UploadSession): UploadState {
  switch (s.status) {
    case "complete":
      return "complete";
    case "processing":
      return "processing";
    case "failed":
      return "failed";
    case "cancelled":
      return "cancelled";
    default:
      return s.offset > 0 ? "paused" : "queued";
  }
}

function patch(localId: string, partial: Partial<UploadJob>) {
  useUploads.setState((st) => ({
    jobs: st.jobs.map((j) => (j.localId === localId ? { ...j, ...partial } : j)),
  }));
}

async function transfer(localId: string) {
  const job = useUploads.getState().jobs.find((j) => j.localId === localId);
  if (!job?.file) {
    patch(localId, { status: "paused", error: "Reselect the file to resume after a refresh." });
    return;
  }
  if (job.size > MAX_UPLOAD_BYTES) {
    patch(localId, { status: "failed", error: "File exceeds the 10 GB limit." });
    return;
  }
  const ac = new AbortController();
  controllers.set(localId, ac);
  paused.delete(localId);
  try {
    let id = job.id;
    let offset = job.offset;
    if (!id) {
      const created = await api.createUpload({
        library_id: job.libraryId,
        filename: job.filename,
        size: job.size,
        size_bytes: job.size,
        mime: job.file.type,
      });
      id = created.id;
      offset = created.offset ?? 0;
      patch(localId, { id, offset, status: "uploading", error: undefined });
    } else {
      const cur = await api.getUpload(id);
      offset = cur.offset;
      patch(localId, { offset, status: cur.status === "complete" ? "complete" : "uploading", itemId: cur.item_id, itemKind: cur.item_kind });
      if (cur.status === "complete") return;
    }
    let lastAt = Date.now();
    let lastOff = offset;
    while (offset < job.size) {
      if (paused.has(localId)) {
        patch(localId, { status: "paused", offset });
        return;
      }
      const end = Math.min(offset + UPLOAD_CHUNK, job.size);
      const chunk = job.file.slice(offset, end);
      patch(localId, { status: offset + chunk.size >= job.size ? "processing" : "uploading" });
      const next = await api.uploadPut(id, chunk, offset, job.size);
      offset = next.offset;
      const now = Date.now();
      const dt = (now - lastAt) / 1000;
      if (dt >= 0.4) {
        const bps = (offset - lastOff) / dt;
        const remain = job.size - offset;
        patch(localId, { offset, bps, etaSec: bps > 0 ? remain / bps : undefined, status: next.status === "complete" ? "complete" : offset >= job.size ? "processing" : "uploading", itemId: next.item_id, itemKind: next.item_kind, filename: next.filename || job.filename });
        lastAt = now;
        lastOff = offset;
      } else {
        patch(localId, { offset, itemId: next.item_id, itemKind: next.item_kind });
      }
      if (next.status === "complete" || next.status === "failed") {
        patch(localId, {
          status: next.status === "complete" ? "complete" : "failed",
          error: next.error,
          itemId: next.item_id,
          itemKind: next.item_kind,
          filename: next.filename || job.filename,
          offset: next.offset,
        });
        return;
      }
    }
  } catch (e) {
    if (ac.signal.aborted || paused.has(localId)) {
      patch(localId, { status: "paused" });
      return;
    }
    const msg = e instanceof ApiError ? e.message : e instanceof Error ? e.message : "upload failed";
    patch(localId, { status: "failed", error: msg });
  } finally {
    controllers.delete(localId);
    void pump();
  }
}

async function pump() {
  if (pumping) return;
  pumping = true;
  try {
    const next = useUploads.getState().jobs.find((j) => j.status === "queued" && j.file);
    if (next) {
      patch(next.localId, { status: "uploading" });
      await transfer(next.localId);
    }
  } finally {
    pumping = false;
    if (useUploads.getState().jobs.some((j) => j.status === "queued" && j.file)) {
      void pump();
    }
  }
}

export const useUploads = create<UploadsStore>((set, get) => ({
  jobs: [],

  enqueue: (files, libraryId, libraryName) => {
    const picked = pickUploadFiles(files);
    const ids: string[] = [];
    const extra: UploadJob[] = [];
    for (const file of picked) {
      const localId = crypto.randomUUID();
      ids.push(localId);
      extra.push({
        localId,
        file,
        filename: file.name,
        size: file.size,
        offset: 0,
        libraryId,
        libraryName,
        status: file.size > MAX_UPLOAD_BYTES ? "failed" : "queued",
        error: file.size > MAX_UPLOAD_BYTES ? "File exceeds the 10 GB limit." : undefined,
      });
    }
    set({ jobs: [...extra, ...get().jobs] });
    void pump();
    return ids;
  },

  pause: (localId) => {
    paused.add(localId);
    controllers.get(localId)?.abort();
    patch(localId, { status: "paused" });
  },

  resume: (localId, file) => {
    const job = get().jobs.find((j) => j.localId === localId);
    if (!job) return;
    if (file) patch(localId, { file, filename: file.name, size: file.size, error: undefined });
    patch(localId, { status: "queued", error: undefined });
    void pump();
  },

  cancel: async (localId) => {
    paused.add(localId);
    controllers.get(localId)?.abort();
    const job = get().jobs.find((j) => j.localId === localId);
    if (job?.id) {
      try {
        await api.cancelUpload(job.id);
      } catch {
        /* already gone */
      }
    }
    patch(localId, { status: "cancelled" });
  },

  retry: (localId) => {
    const job = get().jobs.find((j) => j.localId === localId);
    if (!job) return;
    patch(localId, { status: "queued", error: undefined, id: job.status === "failed" && !job.id ? undefined : job.id });
    void pump();
  },

  attachFile: (localId, file) => {
    patch(localId, { file, filename: file.name, size: file.size, error: undefined });
  },

  hydrate: async () => {
    const remote = await api.listUploads();
    set((st) => {
      const have = new Set(st.jobs.map((j) => j.id).filter(Boolean));
      const extra: UploadJob[] = [];
      for (const s of remote) {
        if (have.has(s.id)) continue;
        extra.push({
          localId: s.id,
          id: s.id,
          filename: s.filename,
          size: s.size,
          offset: s.offset,
          libraryId: s.library_id,
          libraryName: "",
          status: mapServer(s),
          error: s.error,
          itemKind: s.item_kind,
          itemId: s.item_id,
        });
      }
      return { jobs: [...st.jobs, ...extra] };
    });
  },
}));
