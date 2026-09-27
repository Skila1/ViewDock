import { FormEvent, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { contentRatings, MAX_RATING_AGE, TITLE_RATINGS, type RatedTitle } from "@/api/households";
import { hasPerm } from "@/lib/perms";
import { useAuth } from "@/store/auth";

type Props = { kind: "movie" | "series"; id: string; title: RatedTitle };

const UNRATED = "__unrated";
const CUSTOM = "__custom";

/** Rating badge for a title plus an inline override editor for library managers. */
export function ContentRatingControl({ kind, id, title }: Props) {
  const { me } = useAuth();
  const qc = useQueryClient();
  const canEdit = hasPerm(me, "libraries.manage");
  const [editing, setEditing] = useState(false);
  const [choice, setChoice] = useState(() => initialChoice(title));
  const [custom, setCustom] = useState(title.content_rating ?? "");
  const [customAge, setCustomAge] = useState(title.rating_age != null ? String(title.rating_age) : "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const rating = title.content_rating?.trim();
  const badge = rating || (title.rating_age != null ? `${title.rating_age}+` : "");

  const refresh = () => qc.invalidateQueries({ queryKey: [kind, id] });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    let body: { content_rating: string; rating_age?: number | null };
    if (choice === UNRATED) {
      body = { content_rating: "" };
    } else if (choice === CUSTOM) {
      const age = Number(customAge);
      if (!custom.trim() || customAge === "" || !Number.isInteger(age) || age < 0 || age > MAX_RATING_AGE) {
        setErr(`Enter a rating name and a minimum age from 0 to ${MAX_RATING_AGE}.`);
        return;
      }
      body = { content_rating: custom.trim(), rating_age: age };
    } else {
      body = { content_rating: choice };
    }
    setBusy(true);
    try {
      await contentRatings.setTitle(kind, id, body);
      await refresh();
      setEditing(false);
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "could not save the rating");
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setErr("");
    setBusy(true);
    try {
      await contentRatings.resetTitle(kind, id);
      await refresh();
      setEditing(false);
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "could not reset the rating");
    } finally {
      setBusy(false);
    }
  };

  if (!badge && !canEdit) return null;

  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      {badge ? (
        <span
          className="rounded border border-line px-1.5 py-0.5 text-[10px] font-medium"
          title={title.rating_age != null ? `Suitable for ages ${title.rating_age} and up` : "Content rating"}
        >
          {badge}
        </span>
      ) : canEdit ? (
        <span className="rounded border border-dashed border-line px-1.5 py-0.5 text-[10px] text-dim">Unrated</span>
      ) : null}
      {canEdit && !editing ? (
        <button type="button" className="text-[11px] text-accent" onClick={() => setEditing(true)}>
          Edit rating
        </button>
      ) : null}
      {canEdit && editing ? (
        <form onSubmit={save} className="flex flex-wrap items-center gap-2 text-xs">
          <select value={choice} onChange={(e) => setChoice(e.target.value)} aria-label="Content rating">
            {TITLE_RATINGS.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
            <option value={UNRATED}>Unrated</option>
            <option value={CUSTOM}>Custom</option>
          </select>
          {choice === CUSTOM ? (
            <>
              <input className="w-20" placeholder="Rating" value={custom} maxLength={32} onChange={(e) => setCustom(e.target.value)} />
              <input
                className="w-16"
                inputMode="numeric"
                placeholder="Min age"
                value={customAge}
                onChange={(e) => setCustomAge(e.target.value)}
                aria-label="Minimum age"
              />
            </>
          ) : null}
          <button type="submit" className="btn-green rounded-full px-3 py-1" disabled={busy}>
            Save
          </button>
          {title.rating_source === "admin" ? (
            <button type="button" className="text-dim" disabled={busy} onClick={reset} title="Use the TMDB certification again">
              Use TMDB
            </button>
          ) : null}
          <button type="button" className="text-dim" disabled={busy} onClick={() => setEditing(false)}>
            Cancel
          </button>
        </form>
      ) : null}
      {err ? <span className="text-[11px] text-danger">{err}</span> : null}
    </span>
  );
}

function initialChoice(title: RatedTitle): string {
  const r = title.content_rating?.trim() ?? "";
  if (TITLE_RATINGS.includes(r.toUpperCase())) return r.toUpperCase();
  if (r || title.rating_age != null) return CUSTOM;
  return title.rating_source === "admin" ? UNRATED : TITLE_RATINGS[0];
}
