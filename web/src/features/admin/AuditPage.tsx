import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { ChevronDown } from "lucide-react";
import { api, type LogRow } from "@/api/api";
import { CopyButton } from "@/components/CopyButton";
import type { AuditEvent } from "@/types/api.gen";
import { Card, PageHeader, Pill, secondaryBtn } from "./ui";

const PAGE = 50;

type ErrorView = "errors" | "reports" | "warnings";

const ERROR_VIEWS: Record<ErrorView, { label: string; level: string; category: string }> = {
  errors: { label: "All errors", level: "error", category: "" },
  reports: { label: "Player reports", level: "error", category: "client_error" },
  warnings: { label: "Warnings", level: "warn", category: "" },
};

function logText(row: LogRow): string {
  const { trace, ...rest } = row.details ?? {};
  const lines = [
    `Report ID: ${row.id}`,
    `Time: ${row.created_at}`,
    `Level: ${row.level}`,
    `Category: ${row.category}`,
    `Message: ${row.message}`,
  ];
  if (row.actor_id) lines.push(`User: ${row.actor_id}`);
  if (Object.keys(rest).length) lines.push(`Details: ${JSON.stringify(rest, null, 2)}`);
  if (typeof trace === "string" && trace) lines.push("", trace);
  return lines.join("\n");
}

function auditText(e: AuditEvent): string {
  return [
    `Time: ${e.at}`,
    `Action: ${e.action}`,
    `Actor: ${e.actor_username || e.actor_id || "system"}`,
    e.target ? `Target: ${e.target}` : "",
    e.ip ? `IP: ${e.ip}` : "",
    e.detail ? `Detail: ${e.detail}` : "",
  ]
    .filter(Boolean)
    .join("\n");
}

function ErrorRow({ row }: { row: LogRow }) {
  const [open, setOpen] = useState(false);
  const text = logText(row);
  const { trace, ...rest } = row.details ?? {};
  const code = typeof rest.code === "string" ? rest.code : "";
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <p className="flex flex-wrap items-center gap-2 text-xs text-dim">
            <span className="font-mono">{row.created_at}</span>
            <Pill tone={row.level === "error" ? "danger" : "warn"}>{row.level}</Pill>
            <span>{row.category}</span>
            {code ? <span className="font-mono">{code}</span> : null}
          </p>
          <p className="mt-1 break-words text-sm">{row.message}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <CopyButton text={text} className="border-line" />
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-md border border-line px-2.5 py-1 text-xs"
            aria-expanded={open}
            onClick={() => setOpen((v) => !v)}
          >
            Details
            <ChevronDown size={14} className={open ? "rotate-180 transition-transform" : "transition-transform"} aria-hidden />
          </button>
        </div>
      </div>
      {open ? (
        <div className="mt-2 space-y-2 rounded-md border border-line bg-overlay p-2 font-mono text-[11px] leading-snug">
          <p className="text-dim">Report ID {row.id}</p>
          {Object.keys(rest).length ? <pre className="whitespace-pre-wrap break-all">{JSON.stringify(rest, null, 2)}</pre> : null}
          {typeof trace === "string" && trace ? (
            <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all border-t border-line pt-2">{trace}</pre>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

export function AuditPage() {
  const [view, setView] = useState<ErrorView>("errors");
  const [errQ, setErrQ] = useState("");
  const [action, setAction] = useState("");
  const [auditQ, setAuditQ] = useState("");
  const sel = ERROR_VIEWS[view];

  const errors = useInfiniteQuery({
    queryKey: ["admin-audit-errors", view, errQ],
    queryFn: ({ pageParam }) => api.listLogs({ level: sel.level, category: sel.category, q: errQ, limit: PAGE, after: pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => (last.items.length >= PAGE && last.next ? last.next : undefined),
    refetchInterval: 15_000,
  });
  const audit = useInfiniteQuery({
    queryKey: ["admin-audit-events", action, auditQ],
    queryFn: ({ pageParam }) => api.listAudit({ action, q: auditQ, limit: PAGE, before: pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => (last.items.length >= PAGE && last.next ? last.next : undefined),
    refetchInterval: 30_000,
  });

  const errorRows = errors.data?.pages.flatMap((p) => p.items) ?? [];
  const auditRows = audit.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <div className="space-y-4">
      <PageHeader
        title="Audit"
        description={
          <>
            Errors from the server and from players, including the details a viewer sees on the error screen, and every administrative action.
            Scripts and agents can read the same data with a <code>logs.read</code> API key at GET /api/v1/admin/logs?level=error and GET
            /api/v1/admin/audit.
          </>
        }
      />
      <Card
        id="audit-errors"
        title="Errors"
        aside={
          <div className="flex flex-wrap gap-2">
            <select className="text-sm" value={view} onChange={(e) => setView(e.target.value as ErrorView)} aria-label="Error type">
              {(Object.keys(ERROR_VIEWS) as ErrorView[]).map((k) => (
                <option key={k} value={k}>
                  {ERROR_VIEWS[k].label}
                </option>
              ))}
            </select>
            <input className="text-sm" placeholder="search" aria-label="Search errors" value={errQ} onChange={(e) => setErrQ(e.target.value)} />
          </div>
        }
      >
        {errors.isError ? <p className="text-xs text-danger">Errors could not be loaded.</p> : null}
        {errorRows.length ? (
          <ul className="divide-y divide-line rounded-md border border-line">
            {errorRows.map((row) => (
              <ErrorRow key={row.id} row={row} />
            ))}
          </ul>
        ) : null}
        {errors.isSuccess && errorRows.length === 0 ? <p className="text-xs text-dim">No errors recorded.</p> : null}
        {errors.hasNextPage ? (
          <button type="button" className={`${secondaryBtn} mt-3`} disabled={errors.isFetchingNextPage} onClick={() => void errors.fetchNextPage()}>
            {errors.isFetchingNextPage ? "Loading..." : "Load more"}
          </button>
        ) : null}
      </Card>
      <Card
        id="audit-actions"
        title="Admin actions"
        aside={
          <div className="flex flex-wrap gap-2">
            <input
              className="text-sm"
              placeholder="action prefix (api_key., backup.)"
              aria-label="Action prefix"
              value={action}
              onChange={(e) => setAction(e.target.value)}
            />
            <input className="text-sm" placeholder="search" aria-label="Search actions" value={auditQ} onChange={(e) => setAuditQ(e.target.value)} />
          </div>
        }
      >
        {audit.isError ? <p className="text-xs text-danger">Audit events could not be loaded.</p> : null}
        {auditRows.length ? (
          <ul className="divide-y divide-line rounded-md border border-line">
            {auditRows.map((e) => (
              <li key={e.id} className="flex flex-wrap items-start justify-between gap-2 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-2 text-xs text-dim">
                    <span className="font-mono">{e.at}</span>
                    <span>{e.actor_username || e.actor_id || "system"}</span>
                    {e.ip ? <span className="font-mono">{e.ip}</span> : null}
                  </p>
                  <p className="mt-1 break-words text-sm">
                    <span className="font-mono">{e.action}</span>
                    {e.target ? <span className="text-dim"> {e.target}</span> : null}
                  </p>
                  {e.detail ? <p className="mt-0.5 break-words font-mono text-[11px] text-dim">{e.detail}</p> : null}
                </div>
                <CopyButton text={auditText(e)} className="shrink-0 border-line" />
              </li>
            ))}
          </ul>
        ) : null}
        {audit.isSuccess && auditRows.length === 0 ? <p className="text-xs text-dim">No admin actions recorded.</p> : null}
        {audit.hasNextPage ? (
          <button type="button" className={`${secondaryBtn} mt-3`} disabled={audit.isFetchingNextPage} onClick={() => void audit.fetchNextPage()}>
            {audit.isFetchingNextPage ? "Loading..." : "Load more"}
          </button>
        ) : null}
      </Card>
    </div>
  );
}
