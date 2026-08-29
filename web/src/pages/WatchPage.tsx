import { useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { api } from "@/api/api";
import { Player } from "@/components/player/Player";
import { ResumeChoice } from "@/components/player/ResumeChoice";
import { filenameTitle } from "@/lib/format";
import type { ItemKind } from "@/types/api.gen";

export function WatchPage({ kind }: { kind: ItemKind }) {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const hasStart = params.has("t");
  const startMs = Number(params.get("t") || 0) || 0;
  const movie = useQuery({
    queryKey: ["movie", id],
    queryFn: () => api.getMovie(id),
    enabled: kind === "movie" && Boolean(id),
  });
  const episode = useQuery({
    queryKey: ["episode", id],
    queryFn: () => api.getEpisode(id),
    enabled: kind === "episode" && Boolean(id),
  });
  const cont = useQuery({
    queryKey: ["continue"],
    queryFn: api.continueWatching,
    enabled: !hasStart && Boolean(id),
  });

  const title =
    kind === "movie"
      ? filenameTitle(movie.data?.title || "")
      : filenameTitle(episode.data?.title || `S${episode.data?.season ?? 0}E${episode.data?.number ?? 0}`);

  const saved = (cont.data ?? []).find((p) => p.item_kind === kind && p.item_id === id);
  const resumeMs = saved?.resume_ms ?? saved?.position_ms ?? 0;

  const close = () => {
    void queryClient.invalidateQueries({ queryKey: ["continue"] });
    if (kind === "movie") navigate(`/movies/${id}`);
    else if (episode.data?.series_id) navigate(`/tv/${episode.data.series_id}`);
    else navigate(-1);
  };

  if (!hasStart) {
    if (cont.isLoading) {
      return (
        <div className="grid h-dvh w-dvw place-items-center bg-black">
          <Loader2 className="h-10 w-10 animate-spin text-white/80" aria-label="Loading" />
        </div>
      );
    }
    if (resumeMs > 5000) {
      return (
        <ResumeChoice
          title={title}
          resumeMs={resumeMs}
          resumeTo={`?t=${Math.floor(resumeMs)}`}
          startTo="?t=0"
        />
      );
    }
  }

  return (
    <Player
      itemKind={kind}
      itemId={id}
      startMs={startMs}
      title={title}
      onEnded={() => {
        if (kind === "episode" && episode.data?.series_id) {
          void api
            .nextEpisode(episode.data.series_id)
            .then((next) => navigate(`/watch/episode/${next.id}?t=0`))
            .catch(() => navigate(-1));
        }
      }}
      onClose={close}
    />
  );
}
