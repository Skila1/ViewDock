import { useCallback, useState } from "react";
import { Upload } from "lucide-react";
import { api } from "@/api/api";
import type { Library } from "@/types/api.gen";

const CHUNK = 8 * 1024 * 1024;

async function offsetPut(libraryId: string, file: File, onPct: (n: number) => void) {
  const created = await api.createUpload({
    library_id: libraryId,
    filename: file.name,
    size_bytes: file.size,
  });
  let offset = created.offset_bytes ?? 0;
  try {
    const headers = await api.uploadHead(created.id);
    const hdr = headers.get("Upload-Offset") || headers.get("upload-offset");
    if (hdr) offset = Number(hdr);
  } catch {
    /* HEAD optional */
  }
  while (offset < file.size) {
    const end = Math.min(offset + CHUNK, file.size);
    await api.uploadPut(created.id, file.slice(offset, end), offset, file.size);
    offset = end;
    onPct(offset / file.size);
  }
}

export function UploadDropzone({ libraries }: { libraries: Library[] }) {
  const enabled = libraries.filter((l) => l.uploads_enabled);
  const [pct, setPct] = useState<number | null>(null);
  const [err, setErr] = useState("");
  const [over, setOver] = useState(false);

  const run = useCallback(
    async (files: FileList | null) => {
      if (!files?.length || !enabled[0]) return;
      setErr("");
      try {
        for (const file of Array.from(files)) {
          await offsetPut(enabled[0].id, file, setPct);
        }
        setPct(1);
      } catch (e) {
        setErr(e instanceof Error ? e.message : "upload failed");
      }
    },
    [enabled],
  );

  if (!enabled.length) return null;

  return (
    <div
      className={`mb-4 rounded-md border border-dashed px-3 py-4 text-center text-xs ${
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
        void run(e.dataTransfer.files);
      }}
    >
      <Upload size={16} className="mx-auto mb-1 text-dim" />
      <p className="text-dim">Drop files to upload into {enabled[0].name}</p>
      <input
        type="file"
        className="mx-auto mt-2 block text-xs"
        onChange={(e) => void run(e.target.files)}
      />
      {pct !== null ? <p className="mt-2 text-accent">{Math.round(pct * 100)}%</p> : null}
      {err ? <p className="mt-2 text-danger">{err}</p> : null}
    </div>
  );
}
