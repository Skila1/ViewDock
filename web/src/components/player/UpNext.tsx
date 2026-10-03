import { useEffect, useState } from "react";
import { Link } from "react-router";
import { SkipForward, X } from "lucide-react";

/**
 * Up Next card near the end of an episode. With a countdown it plays the next
 * episode when it reaches zero; without one it waits for a click.
 */
export function UpNextCard({ title, seconds, onPlay, onDismiss }: { title?: string; seconds: number | null; onPlay: () => void; onDismiss: () => void }) {
  const [left, setLeft] = useState(seconds);
  useEffect(() => {
    if (seconds == null) return;
    setLeft(seconds);
    const id = window.setInterval(() => setLeft((s) => (s == null ? s : s - 1)), 1000);
    return () => window.clearInterval(id);
  }, [seconds]);
  useEffect(() => {
    if (left != null && left <= 0) onPlay();
  }, [left, onPlay]);

  return (
    <div
      className="pointer-events-auto absolute right-4 bottom-28 z-20 w-72 rounded-lg bg-black/85 p-3 text-white shadow-2xl ring-1 ring-white/15 sm:right-8"
      role="dialog"
      aria-label="Up next"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[11px] tracking-wide text-white/60 uppercase">Up next</p>
          <p className="truncate text-sm font-semibold">{title || "Next episode"}</p>
        </div>
        <button type="button" aria-label="Dismiss" onClick={onDismiss} className="text-white/60 hover:text-white">
          <X size={16} />
        </button>
      </div>
      <button type="button" onClick={onPlay} className="mt-3 flex w-full items-center justify-center gap-2 rounded-md bg-white py-2 text-sm font-semibold text-black hover:bg-white/90">
        <SkipForward size={15} fill="currentColor" strokeWidth={0} />
        {left != null && left > 0 ? `Play in ${left}s` : "Play next"}
      </button>
    </div>
  );
}

export type EndCardTitle = { key: string; title: string; href: string; posterUrl: string | null };

/** After a movie: related titles to keep watching. */
export function EndCard({ titles, onClose }: { titles: EndCardTitle[]; onClose?: () => void }) {
  return (
    <div className="pointer-events-auto absolute inset-0 z-20 flex items-center justify-center bg-black/80 p-6" role="dialog" aria-label="More like this">
      <div className="w-full max-w-4xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold text-white">More like this</h2>
          {onClose ? (
            <button type="button" onClick={onClose} className="rounded-full bg-white/10 px-3 py-1.5 text-sm text-white hover:bg-white/20">
              Close
            </button>
          ) : null}
        </div>
        <div className="grid grid-cols-3 gap-3 sm:grid-cols-6">
          {titles.slice(0, 6).map((t) => (
            <Link key={t.key} to={t.href} className="group block min-w-0">
              <div className="aspect-[2/3] overflow-hidden rounded-md bg-white/10">
                {t.posterUrl ? <img src={t.posterUrl} alt="" className="h-full w-full object-cover transition group-hover:scale-105" /> : null}
              </div>
              <p className="mt-1 truncate text-xs text-white/85">{t.title}</p>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}
