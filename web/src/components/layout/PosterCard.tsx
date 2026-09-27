import { Link } from "react-router";
import { filenameTitle } from "@/lib/format";

type Props = {
  to: string;
  title: string;
  posterUrl?: string | null;
  unmatched?: boolean;
  progress?: number;
};

export function PosterCard({ to, title, posterUrl, unmatched, progress }: Props) {
  const label = filenameTitle(title);
  return (
    <Link to={to} className="group block min-w-0">
      <div className="poster-tile relative overflow-hidden rounded-md bg-raised">
        {posterUrl ? (
          <img src={posterUrl} alt="" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full w-full items-end p-2 text-[12px] leading-tight text-dim">{label}</div>
        )}
        <div className="scrim pointer-events-none absolute inset-x-0 bottom-0 h-1/2" />
        {unmatched ? (
          <span className="absolute top-1 left-1 rounded bg-[var(--chip)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--chip-text)]">
            Unmatched
          </span>
        ) : null}
        {progress && progress > 0 && progress < 0.95 ? (
          <span className="absolute inset-x-0 bottom-0 h-0.5 bg-white/20">
            <span className="block h-full bg-accent" style={{ width: `${Math.round(progress * 100)}%` }} />
          </span>
        ) : null}
      </div>
      <p className="mt-1 truncate text-[12px] text-ink">{label}</p>
    </Link>
  );
}
