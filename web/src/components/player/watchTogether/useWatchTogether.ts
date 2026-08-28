import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/api/api";
import type { WTInvite, WTRoom } from "@/types/api.gen";

export type WTPeer = { id: string; name: string; host?: boolean };

export type WTSync = {
  playing: boolean;
  positionMs: number;
};

type Options = {
  code?: string;
  shareToken?: string;
  guestItem?: { kind: string; id: string };
  onRemote?: (sync: WTSync) => void;
};

export function useWatchTogether(opts: Options) {
  const [invite, setInvite] = useState<WTInvite | null>(null);
  const [room, setRoom] = useState<WTRoom | null>(null);
  const [peers, setPeers] = useState<WTPeer[]>([]);
  const [sync, setSync] = useState<WTSync>({ playing: false, positionMs: 0 });
  const [error, setError] = useState<string | null>(null);
  const [canWatchTogether, setCanWatchTogether] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);

  const itemMatchesShare = useCallback(
    (kind?: string, id?: string) => {
      if (!opts.guestItem) return true;
      if (!kind || !id) return false;
      return kind === opts.guestItem.kind && id === opts.guestItem.id;
    },
    [opts.guestItem],
  );

  const onRemoteRef = useRef(opts.onRemote);
  onRemoteRef.current = opts.onRemote;
  const code = opts.code;
  const guestKind = opts.guestItem?.kind;
  const guestId = opts.guestItem?.id;

  useEffect(() => {
    if (!code) return;
    let cancelled = false;
    (async () => {
      try {
        const inv = await api.getWTInvite(code);
        if (cancelled) return;
        setInvite(inv);
        const match = itemMatchesShare(inv.item_kind, inv.item_id);
        setCanWatchTogether(match);
        if (!match) {
          setError("This room is for a different title.");
          return;
        }
        const joined = await api.joinWT({ code });
        const roomId = joined.room_id || inv.room_id || inv.id || "";
        setRoom({
          id: roomId,
          code,
          item_kind: joined.item_kind ?? inv.item_kind,
          item_id: joined.item_id ?? inv.item_id,
          title: inv.title,
        });
        if (roomId) {
          const ticket = await api.wtTicket(roomId);
          const url = ticket.ws_url || ticket.url;
          if (url) {
            const ws = new WebSocket(url);
            wsRef.current = ws;
            ws.onmessage = (ev) => {
              try {
                const msg = JSON.parse(String(ev.data)) as {
                  type?: string;
                  playing?: boolean;
                  position_ms?: number;
                  peers?: WTPeer[];
                };
                if (msg.type === "peers" && msg.peers) setPeers(msg.peers);
                if (msg.type === "state" || msg.type === "control") {
                  const next = {
                    playing: Boolean(msg.playing),
                    positionMs: msg.position_ms ?? 0,
                  };
                  setSync(next);
                  onRemoteRef.current?.(next);
                }
              } catch {
                /* ignore malformed */
              }
            };
          }
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "watch together failed");
      }
    })();
    return () => {
      cancelled = true;
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [code, guestKind, guestId, itemMatchesShare]);

  const createRoom = useCallback(async (itemKind: string, itemId: string) => {
    const created = await api.createWTRoom({ item_kind: itemKind, item_id: itemId });
    const code = created.code || created.invite_code || created.id;
    setRoom({ ...created, code });
    setCanWatchTogether(true);
    return { ...created, code };
  }, []);

  const send = useCallback((payload: Record<string, unknown>) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(payload));
    }
  }, []);

  return {
    invite,
    room,
    peers,
    sync,
    error,
    canWatchTogether,
    createRoom,
    send,
    sharePath: opts.shareToken
      ? `/s/${opts.shareToken}/together/${opts.code ?? room?.code ?? ""}`
      : `/together/${opts.code ?? room?.code ?? ""}`,
  };
}
