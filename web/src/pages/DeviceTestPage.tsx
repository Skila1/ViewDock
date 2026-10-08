import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { AlertTriangle, CheckCircle2, Info, MonitorSmartphone, RotateCcw, XCircle } from "lucide-react";
import { report } from "@/lib/journey";
import { useAuth } from "@/store/auth";
import {
  GROUP_LABELS,
  loadReport,
  runDeviceTest,
  summarize,
  type CheckGroup,
  type CheckStatus,
  type DeviceCheck,
  type DeviceReport,
} from "@/lib/vault/deviceCheck";

const GROUPS = Object.keys(GROUP_LABELS) as CheckGroup[];

const STATUS_STYLE: Record<CheckStatus, { icon: typeof CheckCircle2; className: string; label: string }> = {
  pass: { icon: CheckCircle2, className: "text-ok", label: "Passed" },
  warn: { icon: AlertTriangle, className: "text-warn", label: "Limited" },
  fail: { icon: XCircle, className: "text-danger", label: "Failed" },
  info: { icon: Info, className: "text-dim", label: "Information" },
};

const EVIDENCE_LABEL = { reported: "Reported by browser", verified: "Verified by test", measured: "Measured" } as const;

export function DeviceTestPage() {
  const userId = useAuth((s) => s.me?.id);
  const [result, setResult] = useState<DeviceReport | null>(() => loadReport());
  const [live, setLive] = useState<DeviceCheck[] | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const started = useRef(false);

  const run = async () => {
    setRunning(true);
    setError("");
    setLive([]);
    try {
      const next = await runDeviceTest(userId, setLive);
      setResult(next);
      const counts = summarize(next.checks);
      const failed = next.checks.filter((c) => c.status === "fail").map((c) => c.id);
      const limited = next.checks.filter((c) => c.status === "warn").map((c) => c.id);
      report("device_test", { pass: counts.pass, warn: counts.warn, fail: counts.fail, failed, limited });
    } catch (err) {
      setError(err instanceof Error ? err.message : "The device test could not finish.");
    } finally {
      setLive(null);
      setRunning(false);
    }
  };

  // First use runs automatically; later visits show the saved result.
  useEffect(() => {
    if (started.current || loadReport()) return;
    started.current = true;
    void run();
  }, []);

  const checks = live ?? result?.checks ?? [];
  const counts = summarize(checks);

  return (
    <section className="mx-auto max-w-3xl space-y-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <MonitorSmartphone className="h-6 w-6 text-accent" />
          <div>
            <h1 className="text-xl font-semibold">Device test</h1>
            <p className="text-sm text-dim">Checks what this browser can play, store and download from this server.</p>
          </div>
        </div>
        <button type="button" className="btn-green tap inline-flex items-center gap-2 rounded-full px-4 text-xs" onClick={() => void run()} disabled={running}>
          <RotateCcw className={`h-4 w-4 ${running ? "animate-spin" : ""}`} />
          {running ? "Testing…" : result ? "Run again" : "Run test"}
        </button>
      </header>

      {error ? <p role="alert" className="rounded-lg border border-danger/40 bg-danger/10 p-3 text-sm text-danger">{error}</p> : null}

      {checks.length ? (
        <div className="flex flex-wrap gap-3 rounded-lg border border-line bg-raised p-3 text-xs" aria-live="polite">
          <span className="text-ok">{counts.pass} passed</span>
          <span className="text-warn">{counts.warn} limited</span>
          <span className="text-danger">{counts.fail} failed</span>
          {result && !running ? <span className="text-dim">Last run {new Date(result.ranAt).toLocaleString()}</span> : null}
        </div>
      ) : running ? null : (
        <p className="text-sm text-dim">No results yet.</p>
      )}

      <p className="text-xs text-dim">
        “Reported by browser” means the browser claims support; only results marked “Verified by test” were exercised on this device. HDR and Dolby Vision are never assumed to work without a verified playback test.
      </p>

      {GROUPS.map((group) => {
        const rows = checks.filter((c) => c.group === group);
        if (!rows.length) return null;
        return (
          <div key={group} className="space-y-2">
            <h2 className="text-sm font-semibold">{GROUP_LABELS[group]}</h2>
            <ul className="divide-y divide-line rounded-lg border border-line bg-raised">
              {rows.map((check) => (
                <CheckRow key={check.id} check={check} />
              ))}
            </ul>
          </div>
        );
      })}

      <p className="text-xs text-dim">
        Save titles for offline viewing in the <Link to="/offline" className="text-accent hover:underline">Offline Vault</Link>.
      </p>
    </section>
  );
}

function CheckRow({ check }: { check: DeviceCheck }) {
  const style = STATUS_STYLE[check.status];
  const Icon = style.icon;
  return (
    <li className="flex items-start gap-3 p-3">
      <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${style.className}`} aria-label={style.label} />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <span className="text-sm font-medium">{check.label}</span>
          <span className="text-[11px] uppercase tracking-wide text-dim">{EVIDENCE_LABEL[check.evidence]}</span>
        </div>
        <p className="text-xs text-dim">{check.detail}</p>
        {check.guidance ? <p className={`text-xs ${check.status === "fail" ? "text-danger" : "text-ink"}`}>{check.guidance}</p> : null}
      </div>
    </li>
  );
}
