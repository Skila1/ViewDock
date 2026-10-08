import * as Dialog from "@radix-ui/react-dialog";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { formatClock } from "@/lib/format";
import type { MenuTarget } from "./TitleMenu";

/** Admin view of a title's files: container, codecs, size and length. */
export function MediaInfoDialog({ target, onClose }: { target: MenuTarget; onClose: () => void }) {
  const movieId = target.kind === "movie" ? target.id : "";
  const movie = useQuery({ queryKey: ["movie", movieId], queryFn: () => api.getMovie(movieId), enabled: Boolean(movieId) });
  const files = movie.data?.files ?? [];
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-[min(520px,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-line bg-raised p-4 shadow-xl">
          <Dialog.Title className="mb-3 text-sm font-medium">Media info{movie.data?.title ? `: ${movie.data.title}` : ""}</Dialog.Title>
          {target.kind !== "movie" ? (
            <p className="text-xs text-dim">File details for shows are listed per episode under Admin, Media, Titles.</p>
          ) : movie.isLoading ? (
            <p className="text-xs text-dim">Loading…</p>
          ) : files.length === 0 ? (
            <p className="text-xs text-dim">No local file. This title streams from a connected media source; its stream details show in the player's stats panel (press I while playing).</p>
          ) : (
            <ul className="space-y-3 text-xs">
              {files.map((f) => (
                <li key={f.id} className="space-y-0.5">
                  <p className="font-medium break-all text-ink">{f.rel_path || f.filename}</p>
                  <p className="text-dim">
                    {[
                      f.container?.toUpperCase(),
                      f.video_codec?.toUpperCase(),
                      f.width && f.height ? `${f.width}x${f.height}` : "",
                      f.audio_codec?.toUpperCase(),
                      f.duration_ms ? formatClock(f.duration_ms) : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </li>
              ))}
            </ul>
          )}
          <div className="mt-4 flex justify-end">
            <Dialog.Close className="rounded-md border border-line px-3 py-1.5 text-xs hover:bg-overlay">Close</Dialog.Close>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
