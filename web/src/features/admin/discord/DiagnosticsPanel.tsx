import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Info, MinusCircle, RefreshCw, XCircle } from "lucide-react";
import { Link } from "react-router";
import type { DiscordCheck, DiscordCheckStatus, DiscordDiagnostics, DiscordGatewayStatus } from "@/api/discordLabs";
import { cn } from "@/lib/cn";
import { formatAge } from "../diagnostics/format";
import { gatewaySummary, groupChecks, modeLabel, overallLabel, statusTone } from "./diagnostics";

function StatusIcon({ status, className }: { status: DiscordCheckStatus; className?: string }) {
  const cls = cn("h-4 w-4 shrink-0", statusTone(status), className);
  switch (status) {
    case "ok":
      return <CheckCircle2 aria-label="OK" className={cls} />;
    case "warn":
      return <AlertTriangle aria-label="Warning" className={cls} />;
    case "error":
      return <XCircle aria-label="Error" className={cls} />;
    case "skipped":
      return <MinusCircle aria-label="Skipped" className={cls} />;
    default:
      return <Info aria-label="Information" className={cls} />;
  }
}

function ActionButton({ check, busy, onAction }: { check: DiscordCheck; busy: boolean; onAction: (c: DiscordCheck) => void }) {
  const a = check.action;
  if (!a) return null;
  const cls = "mt-1 inline-block rounded-full border border-line px-3 py-0.5 text-xs hover:bg-overlay";
  if (a.id === "invite_bot" && a.url) {
    return (
      <a className={cls} href={a.url} target="_blank" rel="noopener noreferrer">
        {a.label}
      </a>
    );
  }
  if (a.id === "open_settings") {
    return (
      <Link className={cls} to="/admin/settings">
        {a.label}
      </Link>
    );
  }
  return (
    <button type="button" className={cls} disabled={busy} onClick={() => onAction(check)}>
      {a.label}
    </button>
  );
}

function GatewayRows({ gateway }: { gateway: DiscordGatewayStatus }) {
  const summary = gatewaySummary(gateway);
  const last = gateway.last_connected_at;
  return (
    <>
      <dt className="text-dim">Gateway</dt>
      <dd className={cn("flex items-center gap-1", statusTone(summary.status))}>
        <StatusIcon status={summary.status} className="h-3.5 w-3.5" />
        {summary.text}
      </dd>
      <dt className="text-dim">Last connected</dt>
      <dd>
        {last ? (
          <span title={new Date(last).toLocaleString()}>{formatAge(last)}</span>
        ) : (
          "Never connected"
        )}
      </dd>
    </>
  );
}

// Matches the xl breakpoint where the panel sits beside the settings.
const SIDE_BY_SIDE = "(min-width: 80rem)";

function useSideBySide() {
  const [wide, setWide] = useState(() => typeof window !== "undefined" && window.matchMedia?.(SIDE_BY_SIDE).matches === true);
  useEffect(() => {
    const mq = window.matchMedia?.(SIDE_BY_SIDE);
    if (!mq) return;
    const update = () => setWide(mq.matches);
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);
  return wide;
}

export function DiagnosticsPanel({
  data,
  loadError,
  running,
  notice,
  actionBusy,
  onRun,
  onAction,
}: {
  data: DiscordDiagnostics | undefined;
  loadError: string;
  running: boolean;
  /** Result of the last run or corrective action. */
  notice: { ok: boolean; text: string } | null;
  actionBusy: boolean;
  onRun: () => void;
  onAction: (c: DiscordCheck) => void;
}) {
  const groups = data ? groupChecks(data.checks) : [];
  const overall = data?.status ?? "info";
  // Above the settings on narrow screens, groups start collapsed so the settings stay close.
  const sideBySide = useSideBySide();
  return (
    <section aria-labelledby="discord-status" className="min-w-0 space-y-3 rounded-lg border border-line bg-raised p-4">
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 id="discord-status" className="text-sm font-semibold">
            Status and diagnostics
          </h2>
          {data ? (
            <p className={cn("mt-0.5 flex items-center gap-1.5 text-sm font-medium", statusTone(overall))}>
              <StatusIcon status={overall} />
              {overallLabel(data)}
            </p>
          ) : null}
        </div>
        <button
          type="button"
          className="btn-green inline-flex items-center gap-1.5 rounded-full px-4 py-1.5 text-sm"
          disabled={running || !data}
          onClick={onRun}
        >
          <RefreshCw className={cn("h-3.5 w-3.5", running && "animate-spin")} />
          {running ? "Checking…" : "Run diagnostics"}
        </button>
      </header>

      {loadError ? <p className="text-xs text-danger">{loadError}</p> : null}
      {!data && !loadError ? <p className="text-xs text-dim">Loading…</p> : null}

      {data ? (
        <>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md bg-overlay p-3 text-xs">
            <dt className="text-dim">Mode</dt>
            <dd>{modeLabel(data.mode)}</dd>
            <dt className="text-dim">Sign-in</dt>
            <dd className="break-all">{data.sign_in_client_id ? `Client ID ${data.sign_in_client_id}` : "No client ID"}</dd>
            <dt className="text-dim">Bot</dt>
            <dd className="break-all">
              {data.application
                ? `${data.application.name} (${data.application.id})`
                : data.mode === "separate"
                  ? "Separate application"
                  : "Sign-in application"}
            </dd>
            {data.gateway ? <GatewayRows gateway={data.gateway} /> : null}
            <dt className="text-dim">Checked</dt>
            <dd>
              {data.checked_at ? (
                <span title={new Date(data.checked_at).toLocaleString()}>
                  {formatAge(data.checked_at)} ({new Date(data.checked_at).toLocaleTimeString()})
                </span>
              ) : (
                "Discord has not been checked yet"
              )}
            </dd>
          </dl>
          {data.stale ? (
            <p className="text-xs text-warn">The configuration changed after the last check. Run diagnostics again for current results.</p>
          ) : null}
          {notice ? (
            <p role="status" className={cn("text-xs", notice.ok ? "text-ok" : "text-danger")}>
              {notice.text}
            </p>
          ) : null}

          <div className="space-y-2">
            {groups.map((g) => (
              <details key={`${g.id}-${sideBySide}`} open={sideBySide && g.open} className="group rounded-md border border-line">
                <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-3 py-2 text-xs font-medium">
                  <span className="flex items-center gap-2">
                    <StatusIcon status={g.status} />
                    {g.label}
                  </span>
                  <span className="text-dim group-open:hidden">{g.checks.length}</span>
                </summary>
                <ul className="divide-y divide-line border-t border-line">
                  {g.checks.map((c) => (
                    <li key={c.id} className="flex gap-2 px-3 py-2 text-xs">
                      <StatusIcon status={c.status} className="mt-0.5" />
                      <div className="min-w-0">
                        <p className="font-medium">{c.label}</p>
                        <p className="break-words text-dim">{c.detail}</p>
                        <ActionButton check={c} busy={actionBusy} onAction={onAction} />
                      </div>
                    </li>
                  ))}
                </ul>
              </details>
            ))}
          </div>
          {!data.checked_at ? (
            <p className="text-[11px] text-dim">
              Configuration checks are shown. Run diagnostics to test the credentials, bot, servers and commands with Discord.
            </p>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
