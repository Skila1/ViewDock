import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Trash2, X } from "lucide-react";
import { api } from "@/api/api";
import { useBrowseData } from "@/components/browse/useBrowse";
import { PosterCard } from "@/components/layout/PosterCard";
import { PosterGrid } from "@/components/layout/PosterGrid";
import { TitleMenu } from "@/components/media/TitleMenu";
import { cn } from "@/lib/cn";
import { formatClock } from "@/lib/format";
import { playHref } from "@/lib/home";
import type { BrowseItem } from "@/lib/browse";
import type { HistoryEntry, ListEntry } from "@/types/api.gen";

type Tab = "list" | "playlists" | "history";
const TABS: { id: Tab; label: string }[] = [
  { id: "list", label: "My List" },
  { id: "playlists", label: "Playlists" },
  { id: "history", label: "Watch history" },
];

/** The profile's own lists: My List, playlists and watch history. */
export function MyListPage() {
  const [params, setParams] = useSearchParams();
  const tab = (TABS.find((t) => t.id === params.get("tab"))?.id ?? "list") as Tab;
  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">My List</h1>
      <div role="tablist" className="flex gap-1 border-b border-line">
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={tab === t.id}
            onClick={() => setParams(t.id === "list" ? {} : { tab: t.id }, { replace: true })}
            className={cn("-mb-px border-b-2 px-3 py-2 text-sm", tab === t.id ? "border-accent text-ink" : "border-transparent text-dim hover:text-ink")}
          >
            {t.label}
          </button>
        ))}
      </div>
      {tab === "list" ? <WatchlistTab /> : tab === "playlists" ? <PlaylistsTab /> : <HistoryTab />}
    </div>
  );
}

function useItems(entries: ListEntry[] | undefined): BrowseItem[] {
  const { items } = useBrowseData();
  return useMemo(() => {
    const byKey = new Map(items.map((it) => [it.key, it]));
    return (entries ?? []).map((e) => byKey.get(`${e.kind}:${e.id}`)).filter((it): it is BrowseItem => Boolean(it));
  }, [items, entries]);
}

function Grid({ items, onRemove }: { items: BrowseItem[]; onRemove?: (it: BrowseItem) => void }) {
  return (
    <PosterGrid>
      {items.map((it) => (
        <div key={it.key} className="group relative">
          <TitleMenu target={{ kind: it.kind, id: it.id, title: it.title }}>
            <PosterCard to={it.href} title={it.title} posterUrl={it.posterUrl} unmatched={it.unmatched} />
          </TitleMenu>
          {onRemove ? (
            <button
              type="button"
              aria-label={`Remove ${it.title}`}
              onClick={() => onRemove(it)}
              className="absolute top-1 right-1 rounded-full bg-black/70 p-1 text-white opacity-0 group-hover:opacity-100 focus:opacity-100"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          ) : null}
        </div>
      ))}
    </PosterGrid>
  );
}

function WatchlistTab() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["watchlist"], queryFn: api.watchlist });
  const items = useItems(list.data);
  if (list.isLoading) return <p className="text-xs text-dim">Loading…</p>;
  if (!items.length) return <p className="text-sm text-dim">Nothing here yet. Right-click any title and choose Add to My List.</p>;
  return (
    <Grid
      items={items}
      onRemove={async (it) => {
        await api.setWatchlist(it.kind, it.id, false);
        await Promise.all([qc.invalidateQueries({ queryKey: ["watchlist"] }), qc.invalidateQueries({ queryKey: ["browse-signals"] })]);
      }}
    />
  );
}

function PlaylistsTab() {
  const qc = useQueryClient();
  const lists = useQuery({ queryKey: ["playlists"], queryFn: api.playlists });
  const [name, setName] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["playlists"] });
  return (
    <div className="space-y-6">
      <form
        className="flex max-w-md gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          if (!name.trim()) return;
          await api.createPlaylist(name.trim());
          setName("");
          await refresh();
        }}
      >
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder="New playlist name" aria-label="New playlist name" className="min-w-0 flex-1" />
        <button type="submit" disabled={!name.trim()} className="rounded-md bg-accent px-3 text-sm text-white disabled:opacity-50">
          Create
        </button>
      </form>
      {lists.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
      {lists.isSuccess && !lists.data.length ? <p className="text-sm text-dim">No playlists yet. Create one here, or right-click a title and choose Add to playlist.</p> : null}
      {(lists.data ?? []).map((p) => (
        <PlaylistSection key={p.id} id={p.id} name={p.name} entries={p.items} onChange={refresh} />
      ))}
    </div>
  );
}

function PlaylistSection({ id, name, entries, onChange }: { id: string; name: string; entries: ListEntry[]; onChange: () => Promise<unknown> }) {
  const items = useItems(entries);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(name);
  return (
    <section className="space-y-2">
      <div className="flex items-center gap-2">
        {editing ? (
          <form
            className="flex gap-2"
            onSubmit={async (e) => {
              e.preventDefault();
              if (!draft.trim()) return;
              await api.renamePlaylist(id, draft.trim());
              setEditing(false);
              await onChange();
            }}
          >
            <input value={draft} onChange={(e) => setDraft(e.target.value)} maxLength={80} aria-label="Playlist name" autoFocus />
            <button type="submit" className="text-xs text-accent">
              Save
            </button>
          </form>
        ) : (
          <h2 className="text-sm font-medium">{name}</h2>
        )}
        <span className="text-xs text-dim">{items.length} titles</span>
        <button type="button" aria-label={`Rename ${name}`} onClick={() => setEditing((v) => !v)} className="text-dim hover:text-ink">
          <Pencil className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          aria-label={`Delete ${name}`}
          onClick={async () => {
            if (!window.confirm(`Delete the playlist "${name}"? The titles stay in your library.`)) return;
            await api.deletePlaylist(id);
            await onChange();
          }}
          className="text-dim hover:text-danger"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
      {items.length ? (
        <Grid
          items={items}
          onRemove={async (it) => {
            await api.removeFromPlaylist(id, it.kind, it.id);
            await onChange();
          }}
        />
      ) : (
        <p className="text-xs text-dim">Empty. Right-click a title and choose Add to playlist.</p>
      )}
    </section>
  );
}

function historyHref(h: HistoryEntry) {
  return playHref(h.item_kind, h.item_id, h.completed ? undefined : h.position_ms);
}

function HistoryTab() {
  const qc = useQueryClient();
  const hist = useInfiniteQuery({
    queryKey: ["history"],
    queryFn: ({ pageParam }) => api.history(pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (last) => (last.length === 100 ? last[last.length - 1].watched_at : undefined),
  });
  const rows = hist.data?.pages.flat() ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["history"] });
  if (hist.isLoading) return <p className="text-xs text-dim">Loading…</p>;
  if (!rows.length) return <p className="text-sm text-dim">Nothing watched yet.</p>;
  return (
    <div className="space-y-3">
      <div className="flex justify-end">
        <button
          type="button"
          className="text-xs text-dim hover:text-danger"
          onClick={async () => {
            if (!window.confirm("Clear your whole watch history? Resume positions are kept.")) return;
            await api.clearHistory();
            await refresh();
          }}
        >
          Clear history
        </button>
      </div>
      <ul className="divide-y divide-line rounded-lg border border-line">
        {rows.map((h) => (
          <li key={h.id} className="flex items-center gap-3 px-3 py-2 text-sm">
            <div className="min-w-0 flex-1">
              <Link to={historyHref(h)} className="block truncate text-ink hover:text-accent">
                {h.title || "Untitled"}
                {h.item_kind === "episode" && h.season != null ? <span className="text-dim"> · S{h.season}:E{h.number}</span> : null}
              </Link>
              <p className="text-xs text-dim">
                {new Date(h.watched_at).toLocaleString()} ·{" "}
                {h.completed ? "Finished" : h.duration_ms ? `Stopped at ${formatClock(h.position_ms)} of ${formatClock(h.duration_ms)}` : "Started"}
              </p>
            </div>
            <button
              type="button"
              aria-label={`Remove ${h.title} from history`}
              className="text-dim hover:text-danger"
              onClick={async () => {
                await api.deleteHistory(h.id);
                await refresh();
              }}
            >
              <X className="h-4 w-4" />
            </button>
          </li>
        ))}
      </ul>
      {hist.hasNextPage ? (
        <button type="button" onClick={() => void hist.fetchNextPage()} className="text-xs text-accent">
          Show older
        </button>
      ) : null}
    </div>
  );
}
