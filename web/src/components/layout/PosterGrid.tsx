import type { ReactNode } from "react";

export function PosterGrid({ children }: { children: ReactNode }) {
  return <div className="poster-grid">{children}</div>;
}
