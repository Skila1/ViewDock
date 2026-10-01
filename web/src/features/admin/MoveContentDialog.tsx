import { useEffect, useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { Library, MoveItemKind, MoveItemRef, MoveJob, MovePlan, MoveRequest } from "@/types/api.gen";
import { destinationOptions, groupIneligible, jobHeadline, planHeadline, typeLabel } from "./moveContent";
import { errText, primaryBtn, secondaryBtn } from "./ui";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  libraries: Library[];
  /** Move every title of this library ("migrate library"). */
  source?: Library;
  /** Or move these titles, which may come from several libraries. */
  items?: (MoveItemRef & { libraryId?: string })[];
  onDone?: () => void;
};

const POLL_MS = 1000;

export function MoveContentDialog({ open, onOpenChange, libraries, source, items, onDone }: Props) {
  const qc = useQueryClient();
  const [dest, setDest] = useState("");
  const [plan, setPlan] = useState<MovePlan | null>(null);
  const [job, setJob] = useState<MoveJob | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const kinds: MoveItemKind[] = useMemo(() => {
    if (source) {
      if (source.content_type === "movies") return ["movie"];
      if (source.content_type === "tv") return ["series"];
      return ["movie", "series"];
    }
    return Array.from(new Set((items ?? []).map((i) => i.kind)));
  }, [source, items]);

  // Hide a library only when everything selected already lives in it.
  const exclude = useMemo(() => {
    if (source) return [source.id];
    const libs = new Set((items ?? []).map((i) => i.libraryId).filter(Boolean) as string[]);
    return libs.size === 1 ? Array.from(libs) : [];
  }, [source, items]);

  const options = useMemo(() => destinationOptions(libraries, kinds, exclude), [libraries, kinds, exclude]);

  const request = (destination: string): MoveRequest =>
    source
      ? { source_library_id: source.id, destination_library_id: destination, all: true }
      : { destination_library_id: destination, items: (items ?? []).map(({ kind, id }) => ({ kind, id })) };

  useEffect(() => {
    if (!open) {
      setDest("");
      setPlan(null);
      setJob(null);
      setErr("");
    }
  }, [open]);

  useEffect(() => {
    setPlan(null);
    setErr("");
    if (!dest || job) return;
    let live = true;
    setBusy(true);
    api
      .previewMove(request(dest))
      .then((p) => live && setPlan(p))
      .catch((e) => live && setErr(errText(e, "the move could not be checked")))
      .finally(() => live && setBusy(false));
    return () => {
      live = false;
    };
  }, [dest]);

  useEffect(() => {
    if (!job || job.status !== "running") return;
    const t = window.setTimeout(async () => {
      try {
        setJob(await api.getMoveJob(job.id));
      } catch (e) {
        setErr(errText(e, "the move status could not be loaded"));
      }
    }, POLL_MS);
    return () => window.clearTimeout(t);
  }, [job]);

  useEffect(() => {
    if (job && job.status !== "running") {
      void qc.invalidateQueries({ queryKey: ["movies"] });
      void qc.invalidateQueries({ queryKey: ["series"] });
      void qc.invalidateQueries({ queryKey: ["libraries"] });
      onDone?.();
    }
  }, [job?.status]);

  const start = async () => {
    if (!dest) return;
    setBusy(true);
    setErr("");
    try {
      setJob(await api.startMove(request(dest)));
    } catch (e) {
      setErr(errText(e, "the move could not start"));
    } finally {
      setBusy(false);
    }
  };

  const chosen = options.find((o) => o.library.id === dest);
  const groups = plan ? groupIneligible(plan.ineligible) : [];
  const renamed = plan?.eligible.filter((i) => i.renamed).length ?? 0;
  const count = source ? `everything in ${source.name}` : `${items?.length ?? 0} selected ${items?.length === 1 ? "title" : "titles"}`;
  const failures = job?.items.filter((i) => i.status === "failed" || i.status === "rolled_back") ?? [];

  return (
    <Dialog.Root open={open} onOpenChange={(v) => (!v && job?.status === "running" ? undefined : onOpenChange(v))}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content
          aria-describedby={undefined}
          className="fixed top-1/2 left-1/2 z-50 max-h-[calc(100dvh-2rem)] w-[min(560px,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 space-y-3 overflow-y-auto rounded-lg border border-line bg-raised p-4 shadow-xl"
        >
          <Dialog.Title className="text-sm font-semibold">{source ? `Move content out of ${source.name}` : "Move to library"}</Dialog.Title>
          <p className="text-xs text-dim">
            Moves {count} on disk, with subtitles, artwork and show and season folders. Watch history, progress, favourites, collections and
            metadata stay with each title. Nothing in the destination is ever replaced.
          </p>

          {!job ? (
            <label className="block text-sm">
              Destination library
              <select className="mt-1 w-full" value={dest} onChange={(e) => setDest(e.target.value)} aria-label="Destination library" disabled={busy && !!dest}>
                <option value="">Choose a library…</option>
                {options.map((o) => (
                  <option key={o.library.id} value={o.library.id} disabled={!o.compatible}>
                    {o.library.name} ({typeLabel(o.library.content_type)}){o.compatible ? "" : " — incompatible"}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {chosen?.note && !job ? <p className="text-xs text-warn">{chosen.note}</p> : null}
          {options.length > 0 && options.every((o) => !o.compatible) ? (
            <p className="text-xs text-warn">No library can hold this content. Create a library of a matching type, or a Mixed library, first.</p>
          ) : null}
          {options.length === 0 ? <p className="text-xs text-warn">Create another library first.</p> : null}

          {busy && !plan && !job ? <p className="text-xs text-dim">Checking…</p> : null}

          {plan && !job ? (
            <div className="space-y-2" data-testid="move-plan">
              <p className="text-sm font-medium" role="status">
                {planHeadline(plan)}
              </p>
              {renamed > 0 ? (
                <p className="text-xs text-dim">
                  {renamed} {renamed === 1 ? "title gets" : "titles get"} a “(2)” name because the name is already taken there.
                </p>
              ) : null}
              {groups.map((g) => (
                <details key={g.reason} className="rounded border border-line p-2 text-xs">
                  <summary className="cursor-pointer">
                    <span className="text-warn">Skipped: {g.message}</span> ({g.titles.length})
                  </summary>
                  <ul className="mt-1 max-h-32 list-disc overflow-y-auto pl-5 text-dim">
                    {g.titles.map((t, i) => (
                      <li key={`${t}-${i}`}>{t}</li>
                    ))}
                  </ul>
                </details>
              ))}
            </div>
          ) : null}

          {job ? (
            <div className="space-y-2">
              <p className="text-sm font-medium" role="status">
                {jobHeadline(job)}
              </p>
              {failures.length > 0 ? (
                <ul className="max-h-40 list-disc space-y-1 overflow-y-auto pl-5 text-xs text-danger">
                  {failures.map((f) => (
                    <li key={`${f.kind}-${f.id}`}>
                      {f.title}: {f.message}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}

          {err ? <p className="text-xs text-danger">{err}</p> : null}

          <div className="flex justify-end gap-2">
            {job && job.status !== "running" ? (
              <Dialog.Close className={primaryBtn}>Done</Dialog.Close>
            ) : (
              <>
                <Dialog.Close className={secondaryBtn} disabled={job?.status === "running"}>
                  Cancel
                </Dialog.Close>
                {!job ? (
                  <button
                    type="button"
                    className={`${primaryBtn} disabled:opacity-50`}
                    disabled={busy || !plan || plan.eligible.length === 0}
                    onClick={() => void start()}
                  >
                    {plan && plan.eligible.length > 0 ? `Move ${plan.eligible.length} ${plan.eligible.length === 1 ? "title" : "titles"}` : "Move"}
                  </button>
                ) : null}
              </>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
