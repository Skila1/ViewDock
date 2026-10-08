import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { Library, MoveJob } from "@/types/api.gen";
import { moveProgress } from "./moveContent";
import { Card } from "./ui";

/** A running move's progress bar, titles, bytes, speed and time left. */
export function MoveProgress({ job }: { job: MoveJob }) {
  const p = moveProgress(job);
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-3 text-sm">
        <span className="min-w-0 truncate font-medium" role="status">
          {p.title}
        </span>
        <span className="shrink-0 text-xs text-dim">{p.percent}%</span>
      </div>
      <div
        className="h-2 overflow-hidden rounded-full bg-overlay"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={p.percent}
        aria-label="Move progress"
      >
        <div className="h-full bg-accent transition-[width] duration-500" style={{ width: `${p.percent}%` }} />
      </div>
      <p className="text-xs text-dim">{p.detail}</p>
    </div>
  );
}

/**
 * The moves running now, shown on the libraries page only while there are
 * any, so a move keeps going after its dialog is closed.
 */
export function ActiveMoves({ libraries }: { libraries: Library[] }) {
  const qc = useQueryClient();
  const moves = useQuery({ queryKey: ["library-moves"], queryFn: api.activeMoves, refetchInterval: 2000 });
  const jobs = moves.data ?? [];
  const had = useRef(false);
  useEffect(() => {
    if (jobs.length > 0) {
      had.current = true;
      return;
    }
    if (had.current) {
      had.current = false;
      void qc.invalidateQueries({ queryKey: ["movies"] });
      void qc.invalidateQueries({ queryKey: ["series"] });
      void qc.invalidateQueries({ queryKey: ["libraries"] });
    }
  }, [jobs.length, qc]);
  if (jobs.length === 0) return null;
  const name = (id?: string) => libraries.find((l) => l.id === id)?.name ?? "another library";
  return (
    <Card id="active-moves" title="Transfers" description="Moves keep running when you leave this page or close their dialog.">
      <div className="space-y-4">
        {jobs.map((job) => (
          <div key={job.id} className="space-y-1">
            <p className="text-xs text-dim">
              {job.source_library_id ? `${name(job.source_library_id)} to ${name(job.destination_library_id)}` : `To ${name(job.destination_library_id)}`}
            </p>
            <MoveProgress job={job} />
          </div>
        ))}
      </div>
    </Card>
  );
}
