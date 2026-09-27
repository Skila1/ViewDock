import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@/api/api";
import { Logo } from "@/components/brand/Logo";
import { activityInstanceId } from "@/lib/discordActivity";
import type { SearchHit } from "@/types/api.gen";

const WAIT_POLL_MS = 5_000;

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : "Something went wrong";
}

function useDebounced(value: string, ms: number): string {
  const [out, setOut] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setOut(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return out;
}

// ActivityPage runs inside the Discord Activity. Everyone in the voice
// channel lands in the same watch party; the first person who can start one
// picks the title.
export function ActivityPage() {
  const navigate = useNavigate();
  const instanceId = activityInstanceId();
  const room = useQuery({
    queryKey: ["activity-room", instanceId],
    queryFn: () => api.activityRoom({ instance_id: instanceId }),
    enabled: Boolean(instanceId),
    retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
    refetchInterval: (q) => (q.state.data && !q.state.data.room ? WAIT_POLL_MS : false),
  });
  const create = useMutation({
    mutationFn: (item: { item_kind: "movie" | "episode"; item_id: string }) =>
      api.activityRoom({ instance_id: instanceId, ...item }),
  });

  const code = create.data?.room?.invite_code ?? room.data?.room?.invite_code;
  useEffect(() => {
    if (code) navigate(`/together/${encodeURIComponent(code)}`, { replace: true });
  }, [code, navigate]);

  let body;
  if (!instanceId) {
    body = <p className="text-sm text-danger">Discord did not pass the Activity details. Close the Activity and start it again.</p>;
  } else if (room.isPending || code) {
    body = <p className="text-sm text-dim">Finding this channel's watch party…</p>;
  } else if (room.isError) {
    body = (
      <>
        <p className="text-sm text-danger">{errMessage(room.error)}</p>
        <button type="button" className="tap w-full rounded-full border border-line text-sm" onClick={() => void room.refetch()}>
          Try again
        </button>
      </>
    );
  } else if (room.data?.can_create) {
    body = (
      <TitlePicker
        busy={create.isPending}
        error={create.isError ? errMessage(create.error) : ""}
        onPick={(item) => create.mutate(item)}
      />
    );
  } else {
    body = (
      <p className="text-sm text-dim">
        Nobody has started a watch party in this channel yet. It opens here as soon as someone picks a title.
      </p>
    );
  }

  return (
    <div className="flex min-h-dvh justify-center bg-bg p-4 pt-[max(1rem,var(--sat))]">
      <div className="w-full max-w-lg space-y-4">
        <div className="flex items-center gap-3">
          <Logo className="h-9 w-9" />
          <h1 className="text-lg font-semibold tracking-tight">Watch together</h1>
        </div>
        <div className="space-y-3 rounded-2xl border border-line bg-raised p-4">{body}</div>
      </div>
    </div>
  );
}

type Item = { item_kind: "movie" | "episode"; item_id: string };

function TitlePicker({ busy, error, onPick }: { busy: boolean; error: string; onPick: (item: Item) => void }) {
  const [q, setQ] = useState("");
  const [series, setSeries] = useState<SearchHit | null>(null);
  const term = useDebounced(q.trim(), 300);
  const results = useQuery({
    queryKey: ["activity-search", term],
    queryFn: () => api.search(term),
    enabled: term.length >= 2 && !series,
  });
  const detail = useQuery({
    queryKey: ["series", series?.item_id],
    queryFn: () => api.getSeries(series!.item_id),
    enabled: Boolean(series),
  });

  if (series) {
    return (
      <div className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-sm font-medium">{series.title}</p>
          <button type="button" className="text-xs text-dim hover:text-ink" onClick={() => setSeries(null)}>
            Back to search
          </button>
        </div>
        {error ? <p className="text-xs text-danger">{error}</p> : null}
        {detail.isPending ? <p className="text-sm text-dim">Loading episodes…</p> : null}
        {detail.isError ? <p className="text-sm text-danger">{errMessage(detail.error)}</p> : null}
        <div className="max-h-[60dvh] space-y-3 overflow-y-auto">
          {(detail.data?.seasons ?? []).map((season) => (
            <div key={season.id ?? season.number}>
              <p className="mb-1 text-xs font-medium text-dim">{season.title || `Season ${season.number}`}</p>
              <ul className="space-y-1">
                {season.episodes.map((ep) => (
                  <li key={ep.id}>
                    <button
                      type="button"
                      disabled={busy}
                      className="w-full truncate rounded-md px-2 py-1.5 text-left text-sm hover:bg-overlay disabled:opacity-50"
                      onClick={() => onPick({ item_kind: "episode", item_id: ep.id })}
                    >
                      {ep.number}. {ep.title}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
    );
  }

  const hits = results.data?.items ?? [];
  return (
    <div className="space-y-3">
      <p className="text-sm text-dim">Pick something for everyone in this channel to watch.</p>
      <input
        type="search"
        className="w-full"
        placeholder="Search movies and shows"
        aria-label="Search movies and shows"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        autoFocus
      />
      {error ? <p className="text-xs text-danger">{error}</p> : null}
      {results.isError ? <p className="text-xs text-danger">{errMessage(results.error)}</p> : null}
      {term.length >= 2 && results.isSuccess && hits.length === 0 ? <p className="text-sm text-dim">No matches.</p> : null}
      <ul className="max-h-[60dvh] space-y-1 overflow-y-auto">
        {hits.map((hit) => (
          <li key={`${hit.item_kind}:${hit.item_id}`}>
            <button
              type="button"
              disabled={busy}
              className="flex w-full items-center gap-3 rounded-md px-2 py-1.5 text-left text-sm hover:bg-overlay disabled:opacity-50"
              onClick={() => (hit.item_kind === "series" ? setSeries(hit) : onPick({ item_kind: hit.item_kind, item_id: hit.item_id }))}
            >
              {hit.poster_url ? <img src={hit.poster_url} alt="" className="h-12 w-8 shrink-0 rounded object-cover" /> : null}
              <span className="min-w-0 flex-1 truncate">
                {hit.title}
                {hit.year ? <span className="text-dim"> ({hit.year})</span> : null}
              </span>
              <span className="text-xs text-dim">{hit.item_kind === "series" ? "Show" : hit.item_kind === "episode" ? "Episode" : "Movie"}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
