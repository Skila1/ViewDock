import { FormEvent, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { api } from "@/api/api";
import { Player } from "@/components/player/Player";
import { useAuth } from "@/store/auth";
import type { ItemKind, ShareMeta, WTInvite } from "@/types/api.gen";

type Props = { guest?: boolean };

export function TogetherPage({ guest }: Props) {
  const { token = "", code = "" } = useParams();
  const navigate = useNavigate();
  const { guest: caps, setGuest } = useAuth();
  const [invite, setInvite] = useState<WTInvite | null>(null);
  const [meta, setMeta] = useState<ShareMeta | null>(null);
  const [unlocked, setUnlocked] = useState(false);
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    void api.getWTInvite(code).then(setInvite).catch((e: Error) => setErr(e.message));
  }, [code]);

  useEffect(() => {
    if (!guest || !token) {
      setUnlocked(true);
      return;
    }
    void api.getShare(token).then(async (m) => {
      setMeta(m);
      if (!m.needs_password) {
        const res = await api.unlockShare(token);
        setGuest({
          shareToken: token,
          itemKind: res.item_kind,
          itemId: res.item_id,
          title: m.title,
          canDownload: Boolean(m.allow_download),
          canWatchTogether: true,
        });
        setUnlocked(true);
      }
    }).catch((e: Error) => setErr(e.message));
  }, [guest, token, setGuest]);

  const onUnlock = async (e: FormEvent) => {
    e.preventDefault();
    try {
      const res = await api.unlockShare(token, { password });
      setGuest({
        shareToken: token,
        itemKind: res.item_kind,
        itemId: res.item_id,
        title: meta?.title,
        canDownload: Boolean(meta?.allow_download),
        canWatchTogether: true,
      });
      setUnlocked(true);
    } catch {
      setErr("Invalid password");
    }
  };

  if (guest && meta?.needs_password && !unlocked) {
    return (
      <div className="flex min-h-dvh items-center justify-center bg-bg p-6 pt-[max(1.5rem,var(--sat))] pb-[max(1.5rem,var(--sab))]">
        <form onSubmit={onUnlock} className="w-full max-w-xs space-y-3 rounded-lg border border-line bg-raised p-5">
          <h1 className="text-base font-medium">Unlock share to join</h1>
          <input
            type="password"
            className="w-full"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Password"
          />
          {err ? <p className="text-xs text-danger">{err}</p> : null}
          <button type="submit" className="w-full rounded-md bg-accent py-2 text-sm text-white">
            Unlock
          </button>
        </form>
      </div>
    );
  }

  if (!invite || !unlocked) {
    return <div className="flex min-h-dvh items-center justify-center text-sm text-dim">{err || "Joining…"}</div>;
  }

  const guestItem = caps
    ? { kind: caps.itemKind, id: caps.itemId }
    : guest
      ? { kind: invite.item_kind, id: invite.item_id }
      : undefined;

  const match = !guestItem || (guestItem.kind === invite.item_kind && guestItem.id === invite.item_id);
  if (guest && !match) {
    return (
      <div className="flex min-h-dvh items-center justify-center p-6 text-sm text-danger">
        This room does not match the shared title.
      </div>
    );
  }

  const kind = (invite.item_kind === "episode" ? "episode" : "movie") as ItemKind;

  return (
    <Player
      itemKind={kind}
      itemId={invite.item_id}
      title={invite.title}
      togetherCode={code}
      shareToken={guest ? token : undefined}
      guestItem={guest ? guestItem : undefined}
      onClose={() => navigate(-1)}
    />
  );
}
