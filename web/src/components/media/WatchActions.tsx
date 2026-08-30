import { Link } from "react-router";
import { Bug, Play, RotateCcw } from "lucide-react";
import { formatClock } from "@/lib/format";
import { withVdDebug } from "@/playback/policy";

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
            className="tap inline-flex items-center gap-1 rounded-md bg-accent px-3 text-sm text-black"
          >
            <Play size={14} /> Resume {formatClock(resumeMs ?? 0)}
          </Link>
          <Link
            to={`${watch}?t=0`}
            className="tap inline-flex items-center gap-1 rounded-md border border-line px-3 text-sm"
          >
            <RotateCcw size={14} /> Play from start
          </Link>
        </>
      ) : (
        <Link
          to={`${watch}?t=0`}
          className="tap inline-flex items-center gap-1 rounded-md bg-accent px-3 text-sm text-black"
        >
          <Play size={14} /> Play
        </Link>
      )}
      <Link
        to={withVdDebug(`${watch}?t=0`)}
        className="tap inline-flex items-center gap-1 rounded-md border border-dashed border-amber-400/70 px-3 text-sm text-amber-200"
      >
        <Bug size={14} /> Play Debug
      </Link>
    </div>
  );
}
