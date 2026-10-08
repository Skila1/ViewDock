import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { ChevronLeft, ChevronRight, CircleArrowRight } from "lucide-react";
import { cn } from "@/lib/cn";

type Props = {
  title: string;
  /** Why the row is here ("From what you watch"), shown under the title. */
  subtitle?: string;
  /** Where the heading leads (the full, filtered list), when there is one. */
  to?: string;
  children: ReactNode;
  className?: string;
};

/** A titled, horizontally scrolling row of cards with previous and next buttons. */
export function MediaRail({ title, subtitle, to, children, className }: Props) {
  const track = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ start: true, end: true });

  const measure = useCallback(() => {
    const el = track.current;
    if (!el) return;
    setEdges({ start: el.scrollLeft <= 2, end: el.scrollLeft + el.clientWidth >= el.scrollWidth - 2 });
  }, []);

  useEffect(() => {
    measure();
    const el = track.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [measure, children]);

  const page = (dir: 1 | -1) => {
    const el = track.current;
    if (el) el.scrollBy({ left: dir * el.clientWidth * 0.9, behavior: "smooth" });
  };

  const arrow = (dir: 1 | -1, disabled: boolean) => {
    const Icon = dir < 0 ? ChevronLeft : ChevronRight;
    return (
      <button
        type="button"
        aria-label={dir < 0 ? `Previous ${title}` : `More ${title}`}
        disabled={disabled}
        onClick={() => page(dir)}
        className="flex h-8 w-8 items-center justify-center rounded-full text-ink hover:bg-overlay disabled:text-dim disabled:opacity-40 disabled:hover:bg-transparent"
      >
        <Icon className="h-5 w-5" />
      </button>
    );
  };

  return (
    <section aria-label={title} className={cn("mb-7", className)}>
      <header className="mb-2 flex items-center justify-between gap-2">
        {to ? (
          <Link to={to} className="group inline-flex items-center gap-2 text-lg font-semibold text-ink hover:text-accent">
            {title}
            <CircleArrowRight className="h-[18px] w-[18px] text-dim group-hover:text-accent" aria-hidden />
          </Link>
        ) : (
          <h2 className="text-lg font-semibold text-ink">{title}</h2>
        )}
        {subtitle ? <span className="mr-auto ml-1 hidden truncate text-xs text-dim sm:inline">{subtitle}</span> : null}
        {edges.start && edges.end ? null : (
          <div className="hidden shrink-0 sm:flex">
            {arrow(-1, edges.start)}
            {arrow(1, edges.end)}
          </div>
        )}
      </header>
      <div ref={track} onScroll={measure} className="rail-scroll flex gap-3 sm:gap-4" role="list">
        {children}
      </div>
    </section>
  );
}

/** One slot in a rail; sizes the card for its shape. */
export function RailItem({ shape, children }: { shape: "wide" | "poster"; children: ReactNode }) {
  return (
    <div role="listitem" className={cn("shrink-0 snap-start", shape === "wide" ? "rail-wide" : "rail-poster")}>
      {children}
    </div>
  );
}
