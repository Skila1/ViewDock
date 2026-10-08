import type { ContentType, Library, MoveItemKind, MoveJob, MovePlan, MovePlanItem } from "@/types/api.gen";

export const CONTENT_TYPES: { value: ContentType; label: string; holds: string }[] = [
  { value: "movies", label: "Movies", holds: "movies only" },
  { value: "tv", label: "TV Shows", holds: "TV shows only" },
  { value: "mixed", label: "Mixed", holds: "movies and TV shows" },
];

export const typeLabel = (t: ContentType) => CONTENT_TYPES.find((c) => c.value === t)?.label ?? t;

/**
 * Mirrors the server's rule (library.Accepts): Movies libraries hold movies,
 * TV Shows libraries hold shows, Mixed libraries hold both. The server
 * enforces it; the web app uses it to offer sensible destinations.
 */
export function accepts(contentType: ContentType, kind: MoveItemKind): boolean {
  if (kind === "movie") return contentType === "movies" || contentType === "mixed";
  return contentType === "tv" || contentType === "mixed";
}

/** The kinds a whole library can contain, from its type. */
export function kindsForLibrary(lib: Library): MoveItemKind[] {
  if (lib.content_type === "movies") return ["movie"];
  if (lib.content_type === "tv") return ["series"];
  return ["movie", "series"];
}

const kindWord = (k: MoveItemKind, n = 2) => (k === "movie" ? (n === 1 ? "movie" : "movies") : n === 1 ? "TV show" : "TV shows");

export type DestinationOption = {
  library: Library;
  compatible: boolean;
  /** Why the destination is limited or unusable, in plain words. */
  note: string;
};

/**
 * Libraries titles of `kinds` can move to. Libraries that accept none of
 * them are still listed, marked incompatible, so it is clear why they
 * cannot be chosen. `exclude` hides the source library.
 */
export function destinationOptions(libraries: Library[], kinds: MoveItemKind[], exclude: string[] = []): DestinationOption[] {
  const want = Array.from(new Set(kinds));
  const out = libraries
    .filter((l) => !exclude.includes(l.id))
    .map((library) => {
      const ok = want.filter((k) => accepts(library.content_type, k));
      const compatible = ok.length > 0;
      let note = "";
      if (!compatible) {
        note = `${typeLabel(library.content_type)} library: cannot hold ${want.map((k) => kindWord(k)).join(" or ")}`;
      } else if (ok.length < want.length) {
        note = `Only ${ok.map((k) => kindWord(k)).join(" and ")} can move here`;
      }
      return { library, compatible, note };
    });
  return out.sort((a, b) => Number(b.compatible) - Number(a.compatible) || a.library.name.localeCompare(b.library.name));
}

export type ReasonGroup = { reason: string; message: string; titles: string[] };

/** Groups the titles a move will skip by reason, for the confirmation step. */
export function groupIneligible(items: MovePlanItem[]): ReasonGroup[] {
  const groups = new Map<string, ReasonGroup>();
  for (const it of items) {
    const key = it.reason ?? "other";
    const label = reasonLabel(it);
    const g = groups.get(key) ?? { reason: key, message: label, titles: [] };
    g.titles.push(it.year ? `${it.title} (${it.year})` : it.title);
    groups.set(key, g);
  }
  return Array.from(groups.values()).sort((a, b) => b.titles.length - a.titles.length);
}

function reasonLabel(it: MovePlanItem): string {
  switch (it.reason) {
    case "incompatible":
      return it.kind === "series" ? "TV shows cannot go into this library" : "Movies cannot go into this library";
    case "duplicate":
      return "Already in the destination library";
    case "same_library":
      return "Already in this library";
    case "missing_files":
      return "Files missing or offline (scan the library first)";
    case "remote":
      return "Managed by an external media server";
    case "no_files":
      return "No files on disk";
    default:
      return it.message || "Cannot be moved";
  }
}

export function planHeadline(plan: MovePlan): string {
  const n = plan.eligible.length;
  const skip = plan.ineligible.length;
  const moving = n === 0 ? "Nothing can move" : `${n} ${n === 1 ? "title" : "titles"} will move (${formatBytes(plan.eligible_bytes)})`;
  return skip > 0 ? `${moving}; ${skip} will be skipped` : moving;
}

export function jobHeadline(job: MoveJob): string {
  if (job.status === "running") {
    const done = job.moved + job.failed;
    const todo = job.total - job.skipped;
    return `Moving… ${done} of ${todo}`;
  }
  const parts = [`${job.moved} moved`];
  if (job.skipped) parts.push(`${job.skipped} skipped`);
  if (job.failed) parts.push(`${job.failed} failed and were left where they were`);
  return parts.join(", ");
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** Where ViewDock will create a library's folder when none is given. */
export function managedFolder(mediaDir: string | undefined, name: string): string {
  const base = (mediaDir || "/media").replace(/\/+$/, "");
  // Same rules as the server (mediafs.FolderName).
  const folder = name
    .replace(/[/\\:\u0000]/g, " ")
    .replace(/[^\p{L}\p{N}\s\-_.,'()&+![\]]/gu, "")
    .split(/\s+/)
    .filter((w) => w.replace(/\./g, "") !== "")
    .join(" ")
    .replace(/^\.+/, "")
    .trim();
  return `${base}/${folder || "Library"}`;
}

/** A running move's progress: how far along it is, and what it is doing. */
export function moveProgress(job: MoveJob): { percent: number; title: string; detail: string } {
  const todo = Math.max(1, job.total - job.skipped);
  const titles = job.moved + job.failed;
  const total = job.bytes_total ?? 0;
  const done = Math.min(job.bytes_done ?? 0, total);
  const percent = total > 0 ? Math.floor((done / total) * 100) : Math.floor((titles / todo) * 100);
  const parts = [`${titles} of ${todo} ${todo === 1 ? "title" : "titles"}`];
  if (total > 0) parts.push(`${formatBytes(done)} of ${formatBytes(total)}`);
  const rate = job.bytes_per_second ?? 0;
  if (rate > 0) {
    parts.push(`${formatBytes(rate)}/s`);
    const left = (total - done) / rate;
    if (total > done && Number.isFinite(left)) parts.push(left < 60 ? "less than a minute left" : `about ${Math.ceil(left / 60)} min left`);
  }
  return { percent, title: job.current ? `Moving ${job.current}` : "Moving…", detail: parts.join(" · ") };
}
