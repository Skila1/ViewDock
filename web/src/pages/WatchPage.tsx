import { useMemo } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { api } from "@/api/api";
import { Player } from "@/components/player/Player";
import type { NowPlayingInfo } from "@/components/player/PauseOverlay";
import { ResumeChoice } from "@/components/player/ResumeChoice";
import { filenameTitle } from "@/lib/format";
import type { ItemKind } from "@/types/api.gen";

export function WatchPage({ kind }: { kind: ItemKind }) {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const location = useLocation();
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
  const seriesId = episode.data?.series_id ?? "";
  const series = useQuery({
    queryKey: ["series", seriesId],
    queryFn: () => api.getSeries(seriesId),
    enabled: kind === "episode" && Boolean(seriesId),
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

  const info = useMemo<NowPlayingInfo | undefined>(() => {
    if (kind === "movie") {
      const m = movie.data;
      if (!m) return undefined;
      return {
        title: filenameTitle(m.title),
        meta: [m.year ? String(m.year) : "", m.content_rating ?? ""].filter(Boolean),
        overview: m.overview,
      };
    }
    const ep = episode.data;
    if (!ep) return undefined;
    const epTitle = filenameTitle(ep.title || "");
    return {
      title: series.data?.title ? filenameTitle(series.data.title) : epTitle || `Episode ${ep.number}`,
      season: `Season ${ep.season}`,
      episode: epTitle && series.data?.title ? `${epTitle}: Ep. ${ep.number}` : `Episode ${ep.number}`,
      overview: ep.overview,
    };
  }, [kind, movie.data, episode.data, series.data]);

  const saved = (cont.data ?? []).find((p) => p.item_kind === kind && p.item_id === id);
  const resumeMs = saved?.resume_ms ?? saved?.position_ms ?? 0;

  const close = () => {
    void queryClient.invalidateQueries({ queryKey: ["continue"] });
    void queryClient.invalidateQueries({ queryKey: ["next-up"] });
    const detail = kind === "movie" ? `/movies/${id}` : episode.data?.series_id ? `/tv/${episode.data.series_id}` : "";
    const from = (location.state as { from?: string } | null)?.from;
    // Inside the Discord Activity, solo playback returns to the Activity's
    // start page, where the viewer can rejoin the channel's party.
    if (from === "/activity") {
      navigate("/activity", { replace: true });
      return;
    }
    // Return to the title page entry the player was opened from, so Back
    // there leads to where the viewer browsed instead of into the player.
    if (!detail || from === detail) navigate(-1);
    else navigate(detail, { replace: true });
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
      title={kind === "episode" && info ? info.title : title}
      info={info}
      onEnded={() => {
        if (kind === "episode" && episode.data?.series_id) {
          void api
            .nextEpisode(episode.data.series_id)
            .then((next) => navigate(`/watch/episode/${next.id}?t=0`, { replace: true, state: location.state }))
            .catch(() => navigate(-1));
        }
      }}
      onClose={close}
    />
  );
}
