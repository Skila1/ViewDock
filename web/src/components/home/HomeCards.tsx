import { Link } from "react-router";
import { Film, Tv } from "lucide-react";
import { filenameTitle } from "@/lib/format";
import type { WideArt } from "@/lib/home";
import { cn } from "@/lib/cn";

function Art({ art, label }: { art: WideArt; label: string }) {
  if (!art.url) {
    return (
      <div className="flex h-full w-full items-center justify-center bg-overlay text-dim">
        <Tv className="h-10 w-10 opacity-50" aria-hidden />
        <span className="sr-only">{label}</span>
      </div>
    );
  }
  if (art.poster) {
    // A poster in a wide frame: blurred fill behind, the poster itself centred.
    return (
      <>
        <img src={art.url} alt="" aria-hidden className="absolute inset-0 h-full w-full scale-110 object-cover opacity-60 blur-xl" />
        <img src={art.url} alt="" loading="lazy" className="relative mx-auto h-full object-contain" />
      </>
    );
  }
  return <img src={art.url} alt="" loading="lazy" className="h-full w-full object-cover" />;
}

/** A 16:9 card with the title and a line under it, centred, as in Continue Watching. */
export function WideCard({
  to,
  title,
  subtitle,
  art,
  progress,
}: {
  to: string;
  title: string;
  subtitle?: string;
  art: WideArt;
  progress?: number;
}) {
  const label = filenameTitle(title);
  return (
    <Link to={to} className="group block min-w-0 text-center" aria-label={subtitle ? `${label}, ${subtitle}` : label}>
      <div className="home-card relative aspect-video overflow-hidden rounded-lg bg-raised">
        <Art art={art} label={label} />
        {progress && progress > 0.01 && progress < 0.97 ? (
          <span className="absolute inset-x-[8%] bottom-[14%] h-1 overflow-hidden rounded-full bg-white/25" aria-hidden>
            <span className="block h-full rounded-full bg-white/90" style={{ width: `${Math.round(progress * 100)}%` }} />
          </span>
        ) : null}
      </div>
      <p className="mt-2 truncate text-sm text-ink">{label}</p>
      {subtitle ? <p className="truncate text-xs text-dim">{subtitle}</p> : null}
    </Link>
  );
}

/** A poster with its title and year under it, centred. */
export function PosterTile({ to, title, subtitle, posterUrl }: { to: string; title: string; subtitle?: string; posterUrl: string | null }) {
  const label = filenameTitle(title);
  return (
    <Link to={to} className="group block min-w-0 text-center">
      <div className="home-card poster-tile relative overflow-hidden rounded-lg bg-raised">
        {posterUrl ? (
          <img src={posterUrl} alt="" loading="lazy" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-dim">
            <Film className="h-10 w-10 opacity-50" aria-hidden />
          </div>
        )}
      </div>
      <p className="mt-2 truncate text-sm text-ink">{label}</p>
      {subtitle ? <p className="truncate text-xs text-dim">{subtitle}</p> : null}
    </Link>
  );
}

/** A library tile: wide artwork, darkened, with the library's name across it. */
export function LibraryTile({ to, label, count, art, artIsPoster }: { to: string; label: string; count?: number; art: string | null; artIsPoster?: boolean }) {
  return (
    <Link
      to={to}
      className="home-card group relative block aspect-video overflow-hidden rounded-lg bg-raised"
      aria-label={count != null ? `${label}, ${count} ${count === 1 ? "title" : "titles"}` : label}
    >
      {art ? (
        <img
          src={art}
          alt=""
          loading="lazy"
          className={cn("absolute inset-0 h-full w-full object-cover transition duration-300 group-hover:scale-105", artIsPoster && "object-[center_25%]")}
        />
      ) : null}
      <span className="absolute inset-0 bg-black/45 transition group-hover:bg-black/30" aria-hidden />
      <span className="absolute inset-0 flex items-center justify-center px-3 text-center text-2xl font-bold tracking-wide text-white drop-shadow-[0_2px_6px_rgb(0_0_0/80%)] sm:text-3xl">
        {label}
      </span>
    </Link>
  );
}
