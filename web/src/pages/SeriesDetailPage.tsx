import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio, Share2 } from "lucide-react";
import { api } from "@/api/api";
import { WatchActions } from "@/components/media/WatchActions";
import { ShareModal } from "@/components/share/ShareModal";
import { filenameTitle, formatClock } from "@/lib/format";
import { hasPerm } from "@/lib/perms";
import { useAuth } from "@/store/auth";

export function SeriesDetailPage() {
  const { id = "" } = useParams();
  const { me } = useAuth();
  const navigate = useNavigate();
  const [share, setShare] = useState(false);
  const q = useQuery({ queryKey: ["series", id], queryFn: () => api.getSeries(id), enabled: Boolean(id) });
  const nextEp = useQuery({
    queryKey: ["next-episode", id],
    queryFn: () => api.nextEpisode(id),
    enabled: Boolean(id),
  });
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching });
  const series = q.data;
  const seasons = series?.seasons ?? [];
  const [season, setSeason] = useState<number>(1);
  useEffect(() => {
    if (seasons[0]) setSeason(seasons[0].number);
  }, [seasons]);
  const episodes = useMemo(
    () => seasons.find((s) => s.number === season)?.episodes ?? seasons[0]?.episodes ?? [],
    [season, seasons],
  );

  if (q.isLoading) return <p className="text-sm text-dim">Loading…</p>;
  if (!series) return <p className="text-sm text-danger">Not found</p>;

  const firstEp = episodes[0];
  const playEp = nextEp.data ?? firstEp;
  const saved = (cont.data ?? []).find((p) => p.item_kind === "episode" && p.item_id === playEp?.id);
  const resumeMs = saved?.resume_ms ?? saved?.position_ms ?? 0;
  const resumeById = new Map(
    (cont.data ?? [])
      .filter((p) => p.item_kind === "episode")
      .map((p) => [p.item_id, p.resume_ms ?? p.position_ms ?? 0]),
  );

  return (
    <div>
      <div className="mb-5 flex flex-col gap-5 sm:flex-row">
        <div className="poster-tile w-[160px] shrink-0 overflow-hidden rounded-md bg-raised">
          {series.poster_url ? (
            <img src={series.poster_url} alt="" className="h-full w-full object-cover" />
          ) : null}
        </div>
        <div>
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold">{filenameTitle(series.title)}</h1>
            {series.unmatched ? (
              <span className="rounded bg-[var(--chip)] px-1.5 py-0.5 text-[10px] text-[var(--chip-text)]">
                Unmatched
              </span>
            ) : null}
          </div>
          {series.overview ? <p className="max-w-2xl text-sm text-dim">{series.overview}</p> : null}
          <div className="mt-4 flex flex-wrap gap-2">
            {playEp ? <WatchActions kind="episode" id={playEp.id} resumeMs={resumeMs} /> : null}
            {playEp ? (
              <button
                type="button"
                className="inline-flex items-center gap-1 rounded-md border border-line px-3 py-1.5 text-sm"
                onClick={async () => {
                  const room = await api.createWTRoom({ item_kind: "episode", item_id: playEp.id });
                  const code = room.code || room.invite_code || room.id;
                  navigate(`/together/${code}`);
                }}
              >
                <Radio size={14} /> Together
              </button>
            ) : null}
            {hasPerm(me, "shares.create") ? (
              <button
                type="button"
                className="inline-flex items-center gap-1 rounded-md border border-line px-3 py-1.5 text-sm"
                onClick={() => setShare(true)}
              >
                <Share2 size={14} /> Share
              </button>
            ) : null}
          </div>
        </div>
      </div>

      {seasons.length > 1 ? (
        <div className="mb-3 flex flex-wrap gap-1">
          {seasons.map((s) => (
            <button
              key={s.number}
              type="button"
              className={`rounded px-2 py-1 text-xs ${season === s.number ? "bg-overlay text-ink" : "text-dim"}`}
              onClick={() => setSeason(s.number)}
            >
              S{s.number}
            </button>
          ))}
        </div>
      ) : null}

      <ul className="divide-y divide-line rounded-md border border-line">
        {episodes.map((ep) => {
          const epResume = resumeById.get(ep.id) ?? 0;
          const to = epResume > 5000 ? `/watch/episode/${ep.id}?t=${Math.floor(epResume)}` : `/watch/episode/${ep.id}?t=0`;
          return (
            <li key={ep.id}>
              <Link to={to} className="flex items-center gap-3 px-3 py-2 hover:bg-raised">
                <span className="w-12 shrink-0 text-xs text-dim">
                  S{ep.season}E{ep.number}
                </span>
                <span className="min-w-0 flex-1 truncate text-sm">{filenameTitle(ep.title || `E${ep.number}`)}</span>
                {epResume > 5000 ? (
                  <span className="shrink-0 text-[11px] text-dim">{formatClock(epResume)}</span>
                ) : null}
                {ep.unmatched ? (
                  <span className="rounded bg-[var(--chip)] px-1.5 py-0.5 text-[10px] text-[var(--chip-text)]">
                    Unmatched
                  </span>
                ) : null}
              </Link>
            </li>
          );
        })}
      </ul>
      <ShareModal open={share} onOpenChange={setShare} itemKind="episode" itemId={playEp?.id ?? series.id} />
    </div>
  );
}
