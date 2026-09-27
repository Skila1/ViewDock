import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

export function errText(e: unknown, fallback: string) {
  return e instanceof Error && e.message ? e.message : fallback;
}

export type Note = { ok: boolean; text: string } | null;

export function NoteLine({ note }: { note: Note }) {
  if (!note) return null;
  return (
    <p role="status" className={note.ok ? "text-xs text-ok" : "text-xs text-danger"}>
      {note.text}
    </p>
  );
}

/** Title, short description and page-level actions at the top of an admin page. */
export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-base font-medium">{title}</h1>
        {description ? <p className="text-sm text-dim">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}

/** Two-column card grid used by every admin page on wide screens. */
export function CardGrid({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("grid min-w-0 items-start gap-4 lg:grid-cols-2", className)}>{children}</div>;
}

export function Card({
  id,
  title,
  description,
  aside,
  className,
  children,
}: {
  id: string;
  title: string;
  description?: ReactNode;
  aside?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className={cn("min-w-0 scroll-mt-4 space-y-3 rounded-lg border border-line bg-raised p-4", className)}>
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 id={`${id}-title`} className="text-sm font-semibold">
            {title}
          </h2>
          {description ? <p className="mt-0.5 text-xs text-dim">{description}</p> : null}
        </div>
        {aside}
      </header>
      {children}
    </section>
  );
}

export function Pill({ tone, children }: { tone: "ok" | "warn" | "dim" | "accent" | "danger"; children: ReactNode }) {
  const cls = { ok: "text-ok", warn: "text-warn", dim: "text-dim", accent: "text-accent", danger: "text-danger" }[tone];
  return <span className={cn("shrink-0 rounded-full bg-overlay px-2 py-0.5 text-[11px] font-medium", cls)}>{children}</span>;
}

export const inputCls = "mt-1 w-full";
export const primaryBtn = "btn-green rounded-full px-4 py-1.5 text-sm";
export const secondaryBtn = "rounded-full border border-line px-4 py-1.5 text-sm";
