export function formatClock(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "0:00";
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

export function filenameTitle(title: string): string {
  return title.replace(/\.[a-z0-9]{2,4}$/i, "").trim() || title;
}

export function displayYear(year: number | null | undefined): string {
  return year && year > 0 ? String(year) : "";
}
