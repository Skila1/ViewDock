import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";
import { api } from "@/api/api";
import { DEFAULT_ROWS, HOME_ROWS, homeRows } from "@/lib/homeRows";

/** Lets a profile pick and order its home rows. Saved to the profile, so every device shows the same home. */
export function CustomizeHome({ current, onClose }: { current: string[] | undefined; onClose: () => void }) {
  const qc = useQueryClient();
  const initial = homeRows(current);
  const [order, setOrder] = useState<string[]>(() => [...initial, ...HOME_ROWS.map((r) => r.id).filter((id) => !initial.includes(id))]);
  const [on, setOn] = useState<Set<string>>(() => new Set(initial));
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState("");
  const label = (id: string) => HOME_ROWS.find((r) => r.id === id)?.label ?? id;

  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= order.length) return;
    const next = [...order];
    [next[i], next[j]] = [next[j], next[i]];
    setOrder(next);
  };

  const save = async (rows: string[]) => {
    setSaving(true);
    setErr("");
    try {
      await api.putPreferences({ home_rows: rows });
      await qc.invalidateQueries({ queryKey: ["prefs"] });
      onClose();
    } catch {
      setErr("Could not save. Try again.");
      setSaving(false);
    }
  };

  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 max-h-[85vh] w-[min(420px,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-lg border border-line bg-raised p-4 shadow-xl">
          <Dialog.Title className="mb-1 text-sm font-medium">Customize home</Dialog.Title>
          <Dialog.Description className="mb-3 text-xs text-dim">Pick the rows you want and their order. This is saved to your profile.</Dialog.Description>
          <ul className="space-y-1">
            {order.map((id, i) => (
              <li key={id} className="flex items-center gap-2 rounded-md px-2 py-1 hover:bg-overlay">
                <label className="flex min-w-0 flex-1 items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={on.has(id)}
                    onChange={(e) => {
                      const next = new Set(on);
                      if (e.target.checked) next.add(id);
                      else next.delete(id);
                      setOn(next);
                    }}
                  />
                  <span className="truncate">{label(id)}</span>
                </label>
                <button type="button" aria-label={`Move ${label(id)} up`} onClick={() => move(i, -1)} disabled={i === 0} className="text-dim hover:text-ink disabled:opacity-30">
                  <ArrowUp className="h-4 w-4" />
                </button>
                <button type="button" aria-label={`Move ${label(id)} down`} onClick={() => move(i, 1)} disabled={i === order.length - 1} className="text-dim hover:text-ink disabled:opacity-30">
                  <ArrowDown className="h-4 w-4" />
                </button>
              </li>
            ))}
          </ul>
          {err ? <p className="mt-2 text-xs text-danger">{err}</p> : null}
          <div className="mt-4 flex justify-between gap-2">
            <button type="button" disabled={saving} onClick={() => void save([])} className="text-xs text-dim hover:text-ink">
              Reset to default
            </button>
            <div className="flex gap-2">
              <Dialog.Close className="rounded-md border border-line px-3 py-1.5 text-xs hover:bg-overlay">Cancel</Dialog.Close>
              <button
                type="button"
                disabled={saving || on.size === 0}
                onClick={() => void save(order.filter((id) => on.has(id)))}
                className="rounded-md bg-accent px-3 py-1.5 text-xs text-white disabled:opacity-50"
              >
                Save
              </button>
            </div>
          </div>
          <p className="mt-2 text-[11px] text-dim">Default: {DEFAULT_ROWS.length} rows, with Next Up shown inside Continue Watching.</p>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
