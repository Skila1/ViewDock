import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio, Share2 } from "lucide-react";
import { api } from "@/api/api";
import { WatchActions } from "@/components/media/WatchActions";
import { ShareModal } from "@/components/share/ShareModal";
import { filenameTitle } from "@/lib/format";
import { hasPerm } from "@/lib/perms";
import { useAuth } from "@/store/auth";

export function MovieDetailPage() {
  const { id = "" } = useParams();
  const { me } = useAuth();
  const navigate = useNavigate();
  const [share, setShare] = useState(false);
  const q = useQuery({ queryKey: ["movie", id], queryFn: () => api.getMovie(id), enabled: Boolean(id) });
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
  const movie = q.data;
  const saved = (cont.data ?? []).find((p) => p.item_kind === "movie" && p.item_id === id);
  const resumeMs = saved?.resume_ms ?? saved?.position_ms ?? 0;
  if (q.isLoading) return <p className="text-sm text-dim">Loading…</p>;
  if (!movie) return <p className="text-sm text-danger">Not found</p>;

  return (
    <div className="flex flex-col gap-5 sm:flex-row">
      <div className="poster-tile mx-auto w-[42%] max-w-[180px] shrink-0 overflow-hidden rounded-md bg-raised sm:mx-0 sm:w-[160px]">
        {movie.poster_url ? (
          <img src={movie.poster_url} alt="" className="h-full w-full object-cover" />
        ) : null}
      </div>
      <div className="min-w-0 flex-1">
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <h1 className="text-xl font-semibold">{filenameTitle(movie.title)}</h1>
          {movie.unmatched ? (
            <span className="rounded bg-[var(--chip)] px-1.5 py-0.5 text-[10px] text-[var(--chip-text)]">
              Unmatched
            </span>
          ) : null}
        </div>
        {movie.year ? <p className="text-sm text-dim">{movie.year}</p> : null}
        {movie.overview ? <p className="mt-3 max-w-2xl text-sm text-dim">{movie.overview}</p> : null}
        <div className="mt-4 flex flex-wrap gap-2">
          <WatchActions kind="movie" id={movie.id} resumeMs={resumeMs} />
          <button
            type="button"
            className="tap inline-flex items-center gap-1 rounded-md border border-line px-3 text-sm"
            onClick={async () => {
              const room = await api.createWTRoom({ item_kind: "movie", item_id: movie.id });
              const code = room.code || room.invite_code || room.id;
              navigate(`/together/${code}`);
            }}
          >
            <Radio size={14} /> Together
          </button>
          {hasPerm(me, "shares.create") ? (
            <button
              type="button"
              className="tap inline-flex items-center gap-1 rounded-md border border-line px-3 text-sm"
              onClick={() => setShare(true)}
            >
              <Share2 size={14} /> Share
            </button>
          ) : null}
        </div>
      </div>
      <ShareModal open={share} onOpenChange={setShare} itemKind="movie" itemId={movie.id} />
    </div>
  );
}
