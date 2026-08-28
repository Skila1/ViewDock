import type { ProgressRecord } from "@/types/api.gen";
import { PosterCard } from "./PosterCard";

export function ContinueStrip({ items }: { items: ProgressRecord[] }) {
  if (!items.length) return null;
  return (
    <section className="mb-5">
      <h2 className="mb-2 text-[13px] font-medium text-dim">Continue watching</h2>
      <div className="continue-slip">
        {items.map((item) => {
          const kind = item.item_kind === "episode" ? "episode" : "movie";
          const pct =
            item.duration_ms > 0 ? (item.resume_ms ?? item.position_ms) / item.duration_ms : 0;
          const t = item.resume_ms ?? item.position_ms;
          return (
            <div key={`${item.item_kind}-${item.item_id}`} className="w-[132px] shrink-0">
              <PosterCard
                to={`/watch/${kind}/${item.item_id}${t ? `?t=${Math.floor(t)}` : ""}`}
                title={item.title || item.item_id}
                posterUrl={item.poster_url}
                unmatched={item.unmatched}
                progress={pct}
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}
