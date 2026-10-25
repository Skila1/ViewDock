import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { ChevronsRight, SkipForward } from "lucide-react";
import { cn } from "@/lib/cn";

/** Bottom-right corner above the control bar, lower while the controls are hidden. */
function corner(raised: boolean) {
  return cn(
    "pointer-events-auto absolute right-4 z-20 flex items-center gap-2 transition-[bottom] duration-200 sm:right-8",
    raised ? "bottom-28 sm:bottom-32" : "bottom-8 sm:bottom-10",
  );
}

/** Shown while a recap or the intro plays; skips to where it ends. */
export function SkipButton({ label, raised, onSkip }: { label: string; raised: boolean; onSkip: () => void }) {
  return (
    <div className={corner(raised)} onClick={(e) => e.stopPropagation()}>
      <button
        type="button"
        onClick={onSkip}
        className="vd-menu-pop flex h-11 items-center gap-2 rounded-md border-2 border-white/80 bg-black/55 px-5 text-[15px] font-semibold text-white shadow-2xl backdrop-blur-sm outline-none transition hover:bg-white hover:text-black focus-visible:ring-2 focus-visible:ring-white/70"
      >
        <ChevronsRight size={18} aria-hidden />
        {label}
      </button>
    </div>
  );
}

/**
 * Next Episode prompt once the episode's story ends (its credits, when they
 * are known). With a countdown the button fills and plays the next episode
 * when it runs out; without one it waits for a click. Watch Credits hides it.
 */
export function NextEpisodePrompt({
  raised,
  label,
  seconds,
  onPlay,
  onDismiss,
}: {
  raised: boolean;
  label?: string;
  seconds: number | null;
  onPlay: () => void;
  onDismiss: () => void;
}) {
  const [filled, setFilled] = useState(false);
  const playRef = useRef(onPlay);
  playRef.current = onPlay;
  useEffect(() => {
    if (seconds == null) return;
    const raf = window.requestAnimationFrame(() => setFilled(true));
    const id = window.setTimeout(() => playRef.current(), seconds * 1000);
    return () => {
      window.cancelAnimationFrame(raf);
      window.clearTimeout(id);
    };
  }, [seconds]);

  return (
    <div className={cn(corner(raised), "flex-col items-end")} role="dialog" aria-label="Next episode" onClick={(e) => e.stopPropagation()}>
      {label ? <p className="vd-menu-pop max-w-72 truncate text-sm font-medium text-white/85 drop-shadow">{label}</p> : null}
      <div className="vd-menu-pop flex items-center gap-2">
        <button
          type="button"
          onClick={onDismiss}
          className="h-11 rounded-md bg-black/55 px-4 text-[15px] font-semibold text-white ring-1 ring-white/40 backdrop-blur-sm outline-none transition hover:bg-black/75 focus-visible:ring-2 focus-visible:ring-white/70"
        >
          Watch Credits
        </button>
        <button
          type="button"
          onClick={onPlay}
          className="relative h-11 overflow-hidden rounded-md bg-white/70 px-5 text-[15px] font-semibold text-black shadow-2xl outline-none focus-visible:ring-2 focus-visible:ring-white/70"
        >
          <span
            aria-hidden
            className="absolute inset-y-0 left-0 bg-white"
            style={{
              width: seconds == null || filled ? "100%" : "0%",
              transition: seconds == null ? undefined : `width ${seconds}s linear`,
            }}
          />
          <span className="relative flex items-center gap-2">
            <SkipForward size={16} fill="currentColor" strokeWidth={0} aria-hidden />
            Next Episode
          </span>
        </button>
      </div>
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
