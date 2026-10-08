import { ArrowLeft } from "lucide-react";
import { useNavigate } from "react-router";

/** Goes back to the previous page in the app, or home when the page was opened directly. */
export function BackButton({ fallback = "/" }: { fallback?: string }) {
  const navigate = useNavigate();
  return (
    <button
      type="button"
      onClick={() => {
        const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
        if (idx > 0) navigate(-1);
        else navigate(fallback, { replace: true });
      }}
      className="tap -ml-2 mb-3 inline-flex items-center gap-1.5 rounded-md px-2 text-sm text-dim transition-colors hover:bg-overlay hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <ArrowLeft size={16} aria-hidden />
      Back
    </button>
  );
}
