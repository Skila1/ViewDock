import { FormEvent, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  diagnosticsApi,
  type OverrideMode,
  type RankDecision,
  type ReliabilityData,
  type SourceReport,
} from "@/api/diagnostics";
import { cn } from "@/lib/cn";
import { errText } from "./FlightTimeline";
import { formatAge, formatDuration, ms, parseCandidates, percent, scoreTone } from "./format";

const MODES: { value: OverrideMode | ""; label: string }[] = [
  { value: "", label: "Automatic" },
  { value: "prefer", label: "Prefer" },
  { value: "avoid", label: "Avoid" },
  { value: "exclude", label: "Exclude" },
];

function OverrideEditor({ source, current, note: initialNote }: { source: string; current?: OverrideMode; note?: string }) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<OverrideMode | "">(current ?? "");
  const [note, setNote] = useState(initialNote ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState("");
  const dirty = mode !== (current ?? "") || note !== (initialNote ?? "");

  const save = async () => {
    setErr("");
    setSaved("");
    setBusy(true);
    try {
      if (mode === "") {
        await diagnosticsApi.clearOverride(source);
        setSaved("Override removed");
      } else {
        const out = await diagnosticsApi.setOverride({ source, mode, note: note.trim() });
        setSaved(out.persisted ? "Saved" : "Applied until the server restarts");
      }
      await qc.invalidateQueries({ queryKey: ["resilience"] });
    } catch (e) {
      setErr(errText(e, "The override could not be saved."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <select aria-label={`Override for ${source}`} value={mode} onChange={(e) => setMode(e.target.value as OverrideMode | "")}>
        {MODES.map((m) => (
          <option key={m.value} value={m.value}>
            {m.label}
          </option>
        ))}
      </select>
      {mode ? (
        <input
          className="min-w-40 flex-1"
          aria-label="Override note"
          placeholder="Reason (optional)"
          maxLength={200}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      ) : null}
      <button type="button" disabled={busy || !dirty} className="rounded-full border border-line px-3 py-1" onClick={() => void save()}>
        {busy ? "Saving…" : "Apply"}
      </button>
      {saved ? <span className="text-ok">{saved}</span> : null}
      {err ? <span className="text-danger">{err}</span> : null}
    </div>
  );
}

function SourceRow({ s, minEvidence }: { s: SourceReport; minEvidence: number }) {
  const m = s.score;
  return (
    <li className="space-y-2 px-3 py-3 text-sm">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="min-w-0 break-all font-mono text-xs">{s.source}</p>
        <p className="text-xs">
          <span className={cn("text-base font-semibold", scoreTone(m.score, m.evidence, minEvidence))}>{m.score.toFixed(1)}</span>
          <span className="text-dim"> / 100</span>
          {s.override ? <span className="ml-2 text-[10px] uppercase tracking-wide text-warn">{s.override.mode}</span> : null}
        </p>
      </div>
      <p className="text-xs text-dim">
        {percent(m.success_rate)} success · {m.stalls_per_session.toFixed(2)} stalls per session · start {ms(m.latency_ms)} · first
        frame {ms(m.ttff_ms)} · {m.evidence.toFixed(1)} weighted sessions
        {m.evidence < minEvidence ? " (limited evidence)" : ""} · last seen {formatAge(s.last_seen)}
      </p>
      {s.scopes.length || s.recent_failures.length ? (
        <details className="text-xs">
          <summary className="cursor-pointer text-dim">
            {s.scopes.length} region or device breakdowns · {s.recent_failures.length} recent failures
          </summary>
          {s.scopes.length ? (
            <table className="mt-2 w-full text-left">
              <thead className="text-dim">
                <tr>
                  <th className="font-normal">Scope</th>
                  <th className="font-normal">Score</th>
                  <th className="font-normal">Success</th>
                  <th className="font-normal">Stalls</th>
                  <th className="font-normal">Evidence</th>
                </tr>
              </thead>
              <tbody>
                {s.scopes.map((sc) => (
                  <tr key={sc.scope} className="border-t border-line">
                    <td className="py-1">{[sc.region && `region ${sc.region}`, sc.device_class && `${sc.device_class} devices`].filter(Boolean).join(", ")}</td>
                    <td className={scoreTone(sc.score, sc.evidence, minEvidence)}>{sc.score.toFixed(1)}</td>
                    <td>{percent(sc.success_rate)}</td>
                    <td>{sc.stalls_per_session.toFixed(2)}</td>
                    <td>{sc.evidence.toFixed(1)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
          {s.recent_failures.length ? (
            <ul className="mt-2 space-y-1">
              {s.recent_failures.map((f, i) => (
                <li key={`${f.at}-${i}`} className="text-danger">
                  {formatAge(f.at)} · {f.reason || f.kind}
                  {f.device_class ? ` · ${f.device_class}` : ""}
                  {f.session_id ? <span className="font-mono text-dim"> · session {f.session_id.slice(0, 8)}</span> : null}
                  {f.request_id ? <span className="font-mono text-dim"> · req {f.request_id}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}
        </details>
      ) : null}
      <OverrideEditor
        key={`${s.override?.mode ?? ""}|${s.override?.note ?? ""}`}
        source={s.source}
        current={s.override?.mode}
        note={s.override?.note}
      />
    </li>
  );
}

function RankExplainer({ defaultCandidates }: { defaultCandidates: string[] }) {
  const [candidates, setCandidates] = useState(defaultCandidates.slice(0, 5).join(", "));
  const [region, setRegion] = useState("");
  const [device, setDevice] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [result, setResult] = useState<RankDecision | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const list = parseCandidates(candidates);
    if (!list.length) {
      setErr("Enter at least one source.");
      return;
    }
    setErr("");
    setBusy(true);
    try {
      setResult(await diagnosticsApi.rank(list, region.trim(), device));
    } catch (e) {
      setResult(null);
      setErr(errText(e, "The ranking could not be computed."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="space-y-3 rounded-md border border-line p-3">
      <p className="text-xs text-dim">Explain how sources would be ranked for a viewer, including administrator overrides.</p>
      <div className="grid gap-2 sm:grid-cols-[1fr_8rem_8rem_auto]">
        <input aria-label="Candidate sources" placeholder="source-a, source-b" value={candidates} onChange={(e) => setCandidates(e.target.value)} />
        <input aria-label="Region" placeholder="Region" value={region} onChange={(e) => setRegion(e.target.value)} />
        <select aria-label="Device class" value={device} onChange={(e) => setDevice(e.target.value)}>
          <option value="">Any device</option>
          <option value="desktop">Desktop</option>
          <option value="mobile">Mobile</option>
          <option value="tablet">Tablet</option>
          <option value="tv">TV</option>
        </select>
        <button type="submit" disabled={busy} className="rounded-full border border-line px-3 py-1 text-sm">
          {busy ? "Ranking…" : "Explain"}
        </button>
      </div>
      {err ? <p className="text-xs text-danger">{err}</p> : null}
      {result ? (
        <div className="space-y-2 text-xs">
          <p className="font-medium">{result.explanation}</p>
          <ol className="space-y-1">
            {result.ranking.map((r) => (
              <li key={r.source} className={r.excluded ? "text-dim line-through" : undefined}>
                <span className="font-mono">{r.source}</span>: {r.explanation}
              </li>
            ))}
          </ol>
        </div>
      ) : null}
    </form>
  );
}

function AddOverride() {
  const [source, setSource] = useState("");
  const trimmed = source.trim();
  return (
    <div className="space-y-2 rounded-md border border-line p-3">
      <p className="text-xs text-dim">Set an override for a source or node that has no observations yet.</p>
      <input className="w-full" aria-label="Source identifier" placeholder="Source or node identifier" maxLength={128} value={source} onChange={(e) => setSource(e.target.value)} />
      {trimmed ? <OverrideEditor key={trimmed} source={trimmed} /> : null}
    </div>
  );
}

export function ReliabilityPanel({ data }: { data: ReliabilityData }) {
  const sources = data.sources ?? [];
  return (
    <div className="space-y-3">
      <p className="text-xs text-dim">
        Scores decay with a half-life of {formatDuration(data.half_life_seconds)}, so old outages stop counting once a source recovers.
        Region and device breakdowns are used once they reach {data.min_evidence} weighted sessions.
      </p>
      {sources.length ? (
        <ul className="divide-y divide-line rounded-md border border-line">
          {sources.map((s) => (
            <SourceRow key={s.source} s={s} minEvidence={data.min_evidence} />
          ))}
        </ul>
      ) : (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">
          No source observations yet. Scores appear after playback sessions start, stall or fail.
        </p>
      )}
      {data.truncated ? <p className="text-xs text-dim">Showing the first {sources.length} sources.</p> : null}
      <div className="grid gap-3 lg:grid-cols-2">
        <RankExplainer defaultCandidates={sources.map((s) => s.source)} />
        <AddOverride />
      </div>
    </div>
  );
}
