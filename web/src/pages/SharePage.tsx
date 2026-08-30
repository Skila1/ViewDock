import { FormEvent, useEffect, useState } from "react";
import { useParams } from "react-router";
import { api } from "@/api/api";
import { Logo } from "@/components/brand/Logo";
import { Player } from "@/components/player/Player";
import { useAuth } from "@/store/auth";
import type { ItemKind, ShareMeta, ShareUnlockResponse } from "@/types/api.gen";

export function SharePage() {
  const { token = "" } = useParams();
  const setGuest = useAuth((s) => s.setGuest);
  const [meta, setMeta] = useState<ShareMeta | null>(null);
  const [unlocked, setUnlocked] = useState<ShareUnlockResponse | null>(null);
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    void api.getShare(token).then(setMeta).catch((e: Error) => setErr(e.message));
  }, [token]);

  useEffect(() => {
    if (meta && !meta.needs_password && !unlocked) {
      void api
        .unlockShare(token)
        .then((res) => {
          setUnlocked(res);
          setGuest({
            shareToken: token,
            itemKind: res.item_kind,
            itemId: res.item_id,
            title: meta.title,
            canDownload: Boolean(meta.allow_download),
            canWatchTogether: false,
          });
        })
        .catch((e: Error) => setErr(e.message));
    }
  }, [meta, token, unlocked, setGuest]);

  const onUnlock = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    try {
      const res = await api.unlockShare(token, { password });
      setUnlocked(res);
      setGuest({
        shareToken: token,
        itemKind: res.item_kind,
        itemId: res.item_id,
        title: meta?.title,
        canDownload: Boolean(meta?.allow_download),
        canWatchTogether: false,
      });
    } catch {
      setErr("Invalid password");
    }
  };

  if (err && !meta) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-danger">{err}</div>;
  }

  if (meta?.needs_password && !unlocked) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg p-6 pt-[max(1.5rem,var(--sat))] pb-[max(1.5rem,var(--sab))]">
        <form onSubmit={onUnlock} className="w-full max-w-xs space-y-3 rounded-lg border border-line bg-raised p-5">
          <Logo className="mx-auto h-16 w-16" />
          <h1 className="text-center text-base font-medium">{meta.title || "Shared video"}</h1>
          <input
            type="password"
            className="w-full"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {err ? <p className="text-xs text-danger">{err}</p> : null}
          <button type="submit" className="tap w-full rounded-md bg-accent text-sm text-black">
            Unlock
          </button>
        </form>
      </div>
    );
  }

  if (!unlocked) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-dim">Opening share…</div>;
  }

  const kind = (unlocked.item_kind === "episode" ? "episode" : "movie") as ItemKind;

  return (
    <Player
      itemKind={kind}
      itemId={unlocked.item_id}
      title={meta?.title}
      shareToken={token}
      guestItem={{ kind: unlocked.item_kind, id: unlocked.item_id }}
    />
  );
}
