import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import type { Cue } from "@/playback/subtitles";

function renderLine(line: string, key: number): ReactNode {
  let italic = false;
  const parts: ReactNode[] = [];
  line.split(/(<\/?i>)/i).forEach((seg, i) => {
    if (/^<i>$/i.test(seg)) italic = true;
    else if (/^<\/i>$/i.test(seg)) italic = false;
    else if (seg) parts.push(italic ? <em key={i}>{seg}</em> : <Fragment key={i}>{seg}</Fragment>);
  });
  return <span key={key} className="block">{parts}</span>;
}

type Props = {
  cues: Cue[];
  /** Raises the text above the control bar while it is shown. */
  lifted: boolean;
};

export function SubtitleOverlay({ cues, lifted }: Props) {
  if (!cues.length) return null;
  return (
    <div
      aria-live="off"
      className={cn(
        "pointer-events-none absolute inset-x-0 z-[6] flex justify-center px-[6vw] transition-[bottom] duration-300 ease-out",
        lifted ? "bottom-[max(7.5rem,calc(var(--sab)+6.5rem))]" : "bottom-[max(3rem,calc(var(--sab)+2rem))]",
      )}
    >
      <p
        className="max-w-[min(64rem,92vw)] text-center text-[clamp(1.05rem,2.3vw,2.15rem)] font-semibold leading-[1.28] text-white"
        style={{ textShadow: "0 0 3px rgba(0,0,0,0.95), 0 2px 6px rgba(0,0,0,0.85), 0 0 18px rgba(0,0,0,0.5)" }}
      >
        {cues.flatMap((c) => c.text.split("\n")).map(renderLine)}
      </p>
    </div>
  );
}
