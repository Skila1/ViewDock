import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { CopyButton } from "@/components/CopyButton";
import { cn } from "@/lib/cn";

export type ReportStatus = { state: "sending" } | { state: "sent"; id: string } | { state: "failed" };

type Props = {
  message: string;
  details: string;
  report: ReportStatus;
  onRetry: () => void;
  onClose?: () => void;
};

export function PlayerErrorPanel({ message, details, report, onRetry, onClose }: Props) {
  const [open, setOpen] = useState(false);
  const copyText = report.state === "sent" ? `Report ID: ${report.id}\n${details}` : details;

  return (
    <div
      className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-3 overflow-y-auto bg-black/70 p-4"
      onClick={(e) => e.stopPropagation()}
      role="alert"
    >
      <p className="max-w-md text-center text-sm text-danger">{message}</p>
      <div className="flex flex-wrap justify-center gap-3">
        <button type="button" className="rounded-md bg-accent px-3 py-1.5 text-sm text-white" onClick={onRetry}>
          Retry
        </button>
        {onClose ? (
          <button type="button" className="rounded-md border border-white/30 px-3 py-1.5 text-sm text-white" onClick={onClose}>
            Exit
          </button>
        ) : null}
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md border border-white/30 px-3 py-1.5 text-sm text-white"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          Details
          <ChevronDown size={16} className={cn("transition-transform", open && "rotate-180")} aria-hidden />
        </button>
      </div>
      {open ? (
        <div className="w-full max-w-2xl rounded-md border border-white/20 bg-black/80 text-white">
          <div className="flex items-center justify-between gap-2 border-b border-white/15 px-3 py-2 text-xs text-white/70">
            <span>
              {report.state === "sent"
                ? `Reported to admins. Report ID ${report.id}`
                : report.state === "sending"
                  ? "Sending report to admins..."
                  : "Could not send the report. Copy the details instead."}
            </span>
            <CopyButton text={copyText} className="border-white/30 text-white" />
          </div>
          <pre className="max-h-[45vh] overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[11px] leading-snug text-white/85">
            {details}
          </pre>
        </div>
      ) : null}
    </div>
  );
}
