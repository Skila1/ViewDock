import { Link } from "react-router";
import { Play, RotateCcw } from "lucide-react";
import { formatClock } from "@/lib/format";
import { report } from "@/lib/journey";

type Props = {
  kind: "movie" | "episode";
  id: string;
  resumeMs?: number | null;
};

export function WatchActions({ kind, id, resumeMs }: Props) {
  const canResume = (resumeMs ?? 0) > 5000;
  const watch = `/watch/${kind}/${id}`;
  return (
    <div className="flex flex-wrap gap-2">
      {canResume ? (
        <>
          <Link
            to={`${watch}?t=${Math.floor(resumeMs ?? 0)}`}
            onClick={() => report("play_click", { kind, id, resume: true, t: Math.floor(resumeMs ?? 0) })}
            className="tap inline-flex items-center gap-1 rounded-md bg-accent px-3 text-sm text-white"
          >
            <Play size={14} /> Resume {formatClock(resumeMs ?? 0)}
          </Link>
          <Link
            to={`${watch}?t=0`}
            onClick={() => report("play_click", { kind, id, resume: false, t: 0 })}
            className="tap inline-flex items-center gap-1 rounded-md border border-line px-3 text-sm"
          >
            <RotateCcw size={14} /> Play from start
          </Link>
        </>
      ) : (
        <Link
          to={`${watch}?t=0`}
          onClick={() => report("play_click", { kind, id, resume: false, t: 0 })}
          className="tap inline-flex items-center gap-1 rounded-md bg-accent px-3 text-sm text-white"
        >
          <Play size={14} /> Play
        </Link>
      )}
    </div>
  );
}
