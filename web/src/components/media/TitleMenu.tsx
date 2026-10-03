import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  BookmarkMinus,
  BookmarkPlus,
  Check,
  Info,
  ListPlus,
  Play,
  RefreshCw,
  RotateCcw,
  Tv,
  EyeOff,
  ThumbsDown,
  ThumbsUp,
  X,
} from "lucide-react";
import { api } from "@/api/api";
import { cn } from "@/lib/cn";
import { formatClock } from "@/lib/format";
import { playHref } from "@/lib/home";
import { hasPerm } from "@/lib/perms";
import { useAuth } from "@/store/auth";
import { MediaInfoDialog } from "./MediaInfoDialog";

export type MenuTarget = {
  kind: "movie" | "series" | "episode";
  id: string;
  /** An episode's show: likes, My List and "Go to series" apply to it. */
  seriesId?: string;
  title?: string;
};

type Point = { x: number; y: number };

/**
 * TitleMenu adds a right-click menu to a title card with the actions that
 * fit its kind: play or resume, My List, playlists, watched state,
 * Continue Watching, likes, and admin tools. The wrapper uses
 * display: contents, so cards keep their layout. Shift+F10 and the context
 * menu key open it from the keyboard too.
 */
export function TitleMenu({ target, children }: { target: MenuTarget; children: ReactNode }) {
  const [at, setAt] = useState<Point | null>(null);
  const [info, setInfo] = useState(false);
  return (
    <div
      className="contents"
      onContextMenu={(e) => {
        e.preventDefault();
        setAt({ x: e.clientX || (e.target as HTMLElement).getBoundingClientRect().left, y: e.clientY || (e.target as HTMLElement).getBoundingClientRect().bottom });
      }}
    >
      {children}
      {at ? <MenuPopup target={target} at={at} onClose={() => setAt(null)} onInfo={() => setInfo(true)} /> : null}
      {info ? <MediaInfoDialog target={target} onClose={() => setInfo(false)} /> : null}
    </div>
  );
}

function MenuPopup({ target, at, onClose, onInfo }: { target: MenuTarget; at: Point; onClose: () => void; onInfo: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { me } = useAuth();
  const signals = useQuery({ queryKey: ["browse-signals"], queryFn: api.browseSignals, staleTime: 60_000 });
  const cont = useQuery({ queryKey: ["continue"], queryFn: api.continueWatching, staleTime: 15_000 });
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState(at);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [lists, setLists] = useState(false);

  const titleKind = target.kind === "episode" ? (target.seriesId ? "series" : null) : target.kind;
  const titleId = target.kind === "episode" ? target.seriesId : target.id;
  const key = titleKind && titleId ? `${titleKind}:${titleId}` : "";
  const has = (list?: string[]) => Boolean(key && list?.includes(key));
  const liked = has(signals.data?.liked);
  const disliked = has(signals.data?.disliked);
  const notInterested = has(signals.data?.not_interested);
  const listed = has(signals.data?.watchlist);
  const finished = target.kind !== "episode" && Boolean(signals.data?.finished?.includes(`${target.kind}:${target.id}`));
  const progress = target.kind === "series" ? undefined : (cont.data ?? []).find((p) => p.item_kind === target.kind && p.item_id === target.id);
  const resumeMs = progress ? (progress.resume_ms ?? progress.position_ms) : 0;
  const admin = hasPerm(me, "libraries.manage");

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setPos({ x: Math.max(8, Math.min(at.x, window.innerWidth - r.width - 8)), y: Math.max(8, Math.min(at.y, window.innerHeight - r.height - 8)) });
  }, [at, lists]);

  useLayoutEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }, []);

  useEffect(() => {
    const away = (e: Event) => {
      if (ref.current && e.target instanceof Node && ref.current.contains(e.target)) return;
      onClose();
    };
    const key = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("pointerdown", away, true);
    window.addEventListener("contextmenu", away, true);
    window.addEventListener("scroll", onClose, true);
    window.addEventListener("resize", onClose);
    window.addEventListener("blur", onClose);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("pointerdown", away, true);
      window.removeEventListener("contextmenu", away, true);
      window.removeEventListener("scroll", onClose, true);
      window.removeEventListener("resize", onClose);
      window.removeEventListener("blur", onClose);
      window.removeEventListener("keydown", key);
    };
  }, [onClose]);

  const run = async (action: () => Promise<unknown>, extra: string[][] = []) => {
    setBusy(true);
    setError("");
    try {
      await action();
      await Promise.all(
        [["browse-signals"], ["continue"], ["next-up"], ["watchlist"], ...extra].map((queryKey) => qc.invalidateQueries({ queryKey })),
      );
      onClose();
    } catch {
      setError("That did not save. Try again.");
      setBusy(false);
    }
  };

  const go = (to: string) => {
    onClose();
    navigate(to);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const items = Array.from(ref.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []);
    const i = items.indexOf(document.activeElement as HTMLButtonElement);
    items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length]?.focus();
  };

  const playable = target.kind !== "series";
  const detailHref = target.kind === "movie" ? `/movies/${target.id}` : `/tv/${target.kind === "series" ? target.id : target.seriesId}`;
  const watchedLabel = target.kind === "episode" ? "Mark episode as watched" : finished ? "Mark as unwatched" : "Mark as watched";

  return createPortal(
    <div
      ref={ref}
      role="menu"
      aria-label={target.title ? `Actions for ${target.title}` : "Title actions"}
      onKeyDown={onKeyDown}
      className="fixed z-[100] max-h-[80vh] w-60 overflow-y-auto rounded-lg border border-line bg-raised p-1 shadow-xl"
      style={{ left: pos.x, top: pos.y }}
    >
      {playable && resumeMs > 5000 ? (
        <MenuItem icon={<Play className="h-4 w-4" />} label={`Resume ${formatClock(resumeMs)}`} onSelect={() => go(playHref(target.kind, target.id, resumeMs))} />
      ) : null}
      {playable ? (
        <MenuItem
          icon={resumeMs > 5000 ? <RotateCcw className="h-4 w-4" /> : <Play className="h-4 w-4" />}
          label={resumeMs > 5000 ? "Play from beginning" : "Play"}
          onSelect={() => go(`/watch/${target.kind}/${target.id}?t=0`)}
        />
      ) : (
        <MenuItem icon={<Play className="h-4 w-4" />} label="Open show" onSelect={() => go(detailHref)} />
      )}
      {target.kind === "episode" && target.seriesId ? <MenuItem icon={<Tv className="h-4 w-4" />} label="Go to series" onSelect={() => go(detailHref)} /> : null}
      {target.kind === "movie" ? <MenuItem icon={<Info className="h-4 w-4" />} label="Details" onSelect={() => go(detailHref)} /> : null}

      <Divider />
      {titleKind && titleId ? (
        <MenuItem
          icon={listed ? <BookmarkMinus className="h-4 w-4" /> : <BookmarkPlus className="h-4 w-4" />}
          label={listed ? "Remove from My List" : target.kind === "episode" ? "Add show to My List" : "Add to My List"}
          disabled={busy}
          onSelect={() => run(() => api.setWatchlist(titleKind, titleId, !listed))}
        />
      ) : null}
      {titleKind && titleId ? (
        <MenuItem icon={<ListPlus className="h-4 w-4" />} label="Add to playlist" expanded={lists} onSelect={() => setLists((v) => !v)} />
      ) : null}
      {lists && titleKind && titleId ? <PlaylistPicker kind={titleKind} id={titleId} busy={busy} run={run} /> : null}

      <Divider />
      <MenuItem
        icon={finished ? <EyeOff className="h-4 w-4" /> : <Check className="h-4 w-4" />}
        label={watchedLabel}
        disabled={busy}
        onSelect={() => run(() => api.setWatched(target.kind, target.id, !finished))}
      />
      {progress && target.kind !== "series" ? (
        <MenuItem
          icon={<X className="h-4 w-4" />}
          label="Remove from Continue Watching"
          disabled={busy}
          onSelect={() => run(() => api.dismissContinue(target.kind as "movie" | "episode", target.id))}
        />
      ) : null}

      {titleKind && titleId ? (
        <>
          <Divider />
          <MenuItem
            icon={<ThumbsUp className={cn("h-4 w-4", liked && "fill-current text-accent")} />}
            label={liked ? "Liked" : "Like"}
            checked={liked}
            disabled={busy}
            onSelect={() => run(() => api.setReaction(titleKind, titleId, liked ? "" : "like"))}
          />
          <MenuItem
            icon={<ThumbsDown className={cn("h-4 w-4", disliked && "fill-current text-danger")} />}
            label={disliked ? "Disliked" : "Dislike"}
            checked={disliked}
            disabled={busy}
            onSelect={() => run(() => api.setReaction(titleKind, titleId, disliked ? "" : "dislike"))}
          />
          <MenuItem
            icon={<Ban className="h-4 w-4" />}
            label={notInterested ? "Not interested (undo)" : "Not interested"}
            checked={notInterested}
            disabled={busy}
            onSelect={() => run(() => api.setReaction(titleKind, titleId, notInterested ? "" : "not_interested"))}
          />
          {target.kind === "episode" ? <p className="px-3 pb-1 text-[11px] text-dim">Ratings and My List apply to the whole show.</p> : null}
        </>
      ) : null}

      {admin && titleKind && titleId ? (
        <>
          <Divider />
          <MenuItem icon={<RefreshCw className="h-4 w-4" />} label="Refresh metadata" disabled={busy} onSelect={() => run(() => api.refreshMetadata(titleKind, titleId), [[titleKind === "movie" ? "movie" : "series", titleId]])} />
          <MenuItem
            icon={<Info className="h-4 w-4" />}
            label="Media info"
            onSelect={() => {
              onClose();
              onInfo();
            }}
          />
        </>
      ) : null}
      {error ? <p className="px-3 py-1 text-xs text-danger">{error}</p> : null}
    </div>,
    document.body,
  );
}

function PlaylistPicker({
  kind,
  id,
  busy,
  run,
}: {
  kind: "movie" | "series";
  id: string;
  busy: boolean;
  run: (action: () => Promise<unknown>, extra?: string[][]) => Promise<void>;
}) {
  const lists = useQuery({ queryKey: ["playlists"], queryFn: api.playlists });
  const [name, setName] = useState("");
  return (
    <div className="mx-2 mb-1 rounded-md border border-line p-1">
      {lists.isLoading ? <p className="px-2 py-1 text-xs text-dim">Loading…</p> : null}
      {(lists.data ?? []).map((p) => {
        const inList = p.items.some((it) => it.kind === kind && it.id === id);
        return (
          <MenuItem
            key={p.id}
            icon={inList ? <Check className="h-4 w-4 text-accent" /> : <span className="h-4 w-4" />}
            label={p.name}
            checked={inList}
            disabled={busy}
            onSelect={() => run(() => (inList ? api.removeFromPlaylist(p.id, kind, id) : api.addToPlaylist(p.id, kind, id)), [["playlists"]])}
          />
        );
      })}
      <form
        className="flex gap-1 p-1"
        onSubmit={(e) => {
          e.preventDefault();
          const n = name.trim();
          if (!n) return;
          void run(async () => {
            const created = await api.createPlaylist(n);
            await api.addToPlaylist(created.id, kind, id);
          }, [["playlists"]]);
        }}
      >
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="New playlist"
          aria-label="New playlist name"
          maxLength={80}
          className="min-w-0 flex-1 rounded border border-line bg-transparent px-2 py-1 text-xs"
        />
        <button type="submit" disabled={busy || !name.trim()} className="rounded bg-accent px-2 text-xs text-white disabled:opacity-50">
          Add
        </button>
      </form>
    </div>
  );
}

function Divider() {
  return <div className="my-1 border-t border-line" />;
}

function MenuItem({
  icon,
  label,
  checked,
  expanded,
  disabled,
  onSelect,
}: {
  icon: ReactNode;
  label: string;
  checked?: boolean;
  expanded?: boolean;
  disabled?: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      role={checked === undefined ? "menuitem" : "menuitemcheckbox"}
      aria-checked={checked}
      aria-expanded={expanded}
      disabled={disabled}
      onClick={onSelect}
      className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-ink hover:bg-overlay focus:bg-overlay focus:outline-none disabled:opacity-50"
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{label}</span>
    </button>
  );
}
