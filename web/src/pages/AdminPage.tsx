import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { UploadDropzone } from "@/components/upload/UploadDropzone";

export function AdminPage() {
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const streams = useQuery({ queryKey: ["streams"], queryFn: api.adminStreams });

  return (
    <div className="space-y-4">
      <h1 className="text-base font-medium">Admin</h1>
      <p className="text-sm text-dim">{streams.data?.length ?? 0} live streams</p>
      <UploadDropzone libraries={libs.data ?? []} />
      <ul className="divide-y divide-line rounded-md border border-line">
        {(libs.data ?? []).map((lib) => (
          <li key={lib.id} className="flex items-center justify-between px-3 py-2 text-sm">
            <span>
              {lib.name}
              <span className="ml-2 text-xs text-dim">{lib.content_type}</span>
            </span>
            <button
              type="button"
              className="text-xs text-accent"
              onClick={() => void api.scanLibrary(lib.id)}
            >
              Scan
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
