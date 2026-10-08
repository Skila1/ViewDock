import { useEffect, useState } from "react";

/** Seconds of the title each preview frame covers (the server's step). */
const PREVIEW_STEP_S = 10;

/** The preview frame URL for a point in the title, on the server's ten second grid. */
export function previewFrameUrl(base: string, ms: number): string {
  const t = Math.max(0, Math.floor(ms / 1000 / PREVIEW_STEP_S) * PREVIEW_STEP_S);
  return `${base}${base.includes("?") ? "&" : "?"}t=${t}`;
}

/**
 * The frame above the seek bar where the pointer is. It keeps showing the
 * last frame until the next one has loaded, so moving along the bar does not
 * flash, and hides itself when the server has none.
 */
export function SeekPreview({ base, ms }: { base: string; ms: number }) {
  const want = previewFrameUrl(base, ms);
  const [shown, setShown] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    const img = new Image();
    img.onload = () => {
      if (!live) return;
      setShown(want);
      setFailed(false);
    };
    img.onerror = () => {
      if (live) setFailed(true);
    };
    img.src = want;
    return () => {
      live = false;
    };
  }, [want]);
  if (!shown || failed) return null;
  return <img src={shown} alt="" draggable={false} className="mb-1 block h-[90px] w-auto max-w-[200px] rounded-md bg-black object-cover shadow-lg ring-1 ring-white/15 sm:h-[112px] sm:max-w-[240px]" />;
}
