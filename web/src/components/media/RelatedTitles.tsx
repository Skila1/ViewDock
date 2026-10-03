import { useMemo } from "react";
import { useBrowseData } from "@/components/browse/useBrowse";
import { PosterCard } from "@/components/layout/PosterCard";
import { relatedTitles } from "@/lib/browse";
import { TitleMenu } from "./TitleMenu";

/** A "More like this" strip for a title page, drawn from the viewer's catalogue. */
export function RelatedTitles({ kind, id }: { kind: "movie" | "series"; id: string }) {
  const { items, loading } = useBrowseData();
  const related = useMemo(() => relatedTitles(items, `${kind}:${id}`), [items, kind, id]);
  if (loading || !related.length) return null;
  return (
    <section className="mt-8" aria-labelledby="related-heading">
      <h2 id="related-heading" className="mb-2 text-[13px] font-medium text-dim">
        More like this
      </h2>
      <div className="continue-slip">
        {related.map((it) => (
          <div key={it.key} className="w-[132px] shrink-0">
            <TitleMenu target={{ kind: it.kind, id: it.id }}>
              <PosterCard to={it.href} title={it.title} posterUrl={it.posterUrl} unmatched={it.unmatched} />
            </TitleMenu>
          </div>
        ))}
      </div>
    </section>
  );
}
