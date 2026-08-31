import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { api } from "@/api/api";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  itemKind: string;
  itemId: string;
};

export function ShareModal({ open, onOpenChange, itemKind, itemId }: Props) {
  const [password, setPassword] = useState("");
  const [hours, setHours] = useState("");
  const [download, setDownload] = useState(false);
  const [link, setLink] = useState("");
  const [err, setErr] = useState("");

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/60" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-[min(420px,calc(100%-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-line bg-raised p-4 shadow-xl">
          <Dialog.Title className="mb-3 text-sm font-medium">Share</Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={async (e) => {
              e.preventDefault();
              setErr("");
              try {
                const res = await api.createShare({
                  item_kind: itemKind,
                  item_id: itemId,
                  password: password || undefined,
                  hours: hours ? Number(hours) : undefined,
                  allow_download: download,
                });
                setLink(`${window.location.origin}/s/${res.token}`);
              } catch (e2) {
                setErr(e2 instanceof Error ? e2.message : "share failed");
              }
            }}
          >
            <label className="block text-xs text-dim">
              Password (optional)
              <input
                className="mt-1 w-full"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
            <label className="block text-xs text-dim">
              Expires in hours
              <input
                className="mt-1 w-full"
                type="number"
                min={0}
                value={hours}
                onChange={(e) => setHours(e.target.value)}
              />
            </label>
            <label className="flex items-center gap-2 text-xs">
              <input type="checkbox" checked={download} onChange={(e) => setDownload(e.target.checked)} />
              Allow download
            </label>
            {err ? <p className="text-xs text-danger">{err}</p> : null}
            {link ? (
              <p className="break-all rounded bg-bg px-2 py-1 text-xs">{link}</p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Dialog.Close className="rounded border border-line px-3 py-1.5 text-xs">Cancel</Dialog.Close>
              <button type="submit" className="rounded bg-accent px-3 py-1.5 text-xs text-white">
                Create link
              </button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
