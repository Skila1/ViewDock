import { Link } from "react-router";
import { Bug, Play, RotateCcw } from "lucide-react";
import { formatClock } from "@/lib/format";
import { withVdDebug } from "@/playback/policy";

type Props = {
  title?: string;
  resumeMs: number;
  resumeTo: string;
  startTo: string;
};

export function ResumeChoice({ title, resumeMs, resumeTo, startTo }: Props) {
  return (
    <div className="flex h-dvh w-dvw flex-col items-center justify-center gap-5 bg-black px-6 pt-[var(--sat)] pb-[var(--sab)]">
      {title ? <p className="max-w-lg text-center text-lg font-medium text-white">{title}</p> : null}
      <p className="text-sm text-white/60">You left off at {formatClock(resumeMs)}.</p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Link
          to={resumeTo}
          replace
          className="tap inline-flex items-center gap-2 rounded-md bg-accent px-4 text-sm font-medium text-black"
        >
          <Play size={16} /> Resume
        </Link>
        <Link
          to={startTo}
          replace
          className="tap inline-flex items-center gap-2 rounded-md border border-white/25 px-4 text-sm text-white"
        >
          <RotateCcw size={16} /> Play from start
        </Link>
        <Link
          to={withVdDebug(startTo)}
          replace
          className="tap inline-flex items-center gap-2 rounded-md border border-dashed border-amber-400/70 px-4 text-sm text-amber-200"
        >
          <Bug size={16} /> Play Debug
        </Link>
      </div>
    </div>
  );
}
