import { useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Player } from "@/components/player/Player";
import { filenameTitle } from "@/lib/format";
import type { ItemKind } from "@/types/api.gen";

export function WatchPage({ kind }: { kind: ItemKind }) {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
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

  const title =
    kind === "movie"
      ? filenameTitle(movie.data?.title || "")
      : filenameTitle(episode.data?.title || `S${episode.data?.season ?? 0}E${episode.data?.number ?? 0}`);

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
            .then((next) => navigate(`/watch/episode/${next.id}`))
            .catch(() => navigate(-1));
        }
      }}
    />
  );
}
