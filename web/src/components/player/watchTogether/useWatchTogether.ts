import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/api/api";
import type { WTInvite, WTRoom } from "@/types/api.gen";
import {
  ClockSync,
  parseCorrection,
  projectTimeline,
  reconnectDelay,
  type PartyCorrection,
  type PartyMember,
  type PartyState,
  type PartySyncInfo,
} from "./partySync";

export type WTPeer = { id: string; name: string; host?: boolean };

export type WTSync = {
  playing: boolean;
  positionMs: number;
};

export type PartyStatus = "idle" | "connecting" | "synced" | "reconnecting" | "ended";

export type LocalPlayback = {
  positionMs: number;
  playing: boolean;
  buffering: boolean;
  ready: boolean;
};

/** Room timeline projected to the moment it is delivered. */
export type PartyTimeline = { playing: boolean; positionMs: number; reason?: string; by?: string; seq: number };

type Options = {
  code?: string;
  shareToken?: string;
  guestItem?: { kind: string; id: string };
  getLocal?: () => LocalPlayback | null;
  onTimeline?: (t: PartyTimeline) => void;
  onCorrection?: (c: PartyCorrection) => void;
};

const REPORT_MS = 1000;
const PING_MS = 15_000;
const FAST_PINGS = 5;

function wsUrl(path: string): string {
  const u = new URL(path, window.location.href);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  return u.toString();
}

export function useWatchTogether(opts: Options) {
  const [invite, setInvite] = useState<WTInvite | null>(null);
  const [room, setRoom] = useState<WTRoom | null>(null);
  const [members, setMembers] = useState<PartyMember[]>([]);
  const [hostId, setHostId] = useState("");
  const [memberId, setMemberId] = useState("");
  const [sharedControl, setSharedControl] = useState(false);
  const [syncInfo, setSyncInfo] = useState<PartySyncInfo | null>(null);
  const [sync, setSync] = useState<WTSync>({ playing: false, positionMs: 0 });
  const [status, setStatus] = useState<PartyStatus>("idle");
  const [lastCorrection, setLastCorrection] = useState<PartyCorrection | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [canWatchTogether, setCanWatchTogether] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const clockRef = useRef(new ClockSync());

  const getLocalRef = useRef(opts.getLocal);
  getLocalRef.current = opts.getLocal;
  const onTimelineRef = useRef(opts.onTimeline);
  onTimelineRef.current = opts.onTimeline;
  const onCorrectionRef = useRef(opts.onCorrection);
  onCorrectionRef.current = opts.onCorrection;

  const code = opts.code;
  const guestKind = opts.guestItem?.kind;
  const guestId = opts.guestItem?.id;

  const send = useCallback((payload: Record<string, unknown>) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(payload));
      return true;
    }
    return false;
  }, []);

  useEffect(() => {
    if (!code) return;
    let disposed = false;
    let attempt = 0;
    let retryTimer = 0;
    let reportTimer = 0;
    let pingTimer = 0;
    let lastReady: boolean | null = null;
    const clock = clockRef.current;

    const ping = () => send({ type: "ping", t0: Date.now() });

    const report = () => {
      const local = getLocalRef.current?.();
      if (!local) return;
      if (local.ready !== lastReady && send({ type: local.ready ? "ready" : "unready" })) {
        lastReady = local.ready;
      }
      send({
        type: "position",
        position_ms: Math.max(0, Math.floor(local.positionMs)),
        playing: local.playing,
        buffering: local.buffering || !local.ready,
        at_server_ms: clock.ready ? Math.floor(clock.serverNow()) : 0,
      });
    };

    const applyState = (msg: PartyState) => {
      setMembers(msg.members ?? []);
      setHostId(msg.host ?? "");
      setSharedControl(Boolean(msg.shared_control));
      if (msg.sync) setSyncInfo(msg.sync);
      const positionMs = projectTimeline(msg.position_ms ?? 0, msg.server_ms ?? 0, Boolean(msg.playing), clock.serverNow());
      const next = { playing: Boolean(msg.playing), positionMs };
      setSync(next);
      onTimelineRef.current?.({ ...next, reason: msg.reason, by: msg.by, seq: msg.seq ?? 0 });
    };

    const stopTimers = () => {
      window.clearInterval(reportTimer);
      window.clearInterval(pingTimer);
    };

    const scheduleReconnect = () => {
      if (disposed) return;
      setStatus("reconnecting");
      const delay = reconnectDelay(attempt++);
      retryTimer = window.setTimeout(() => void connect(), delay);
    };

    const connect = async () => {
      if (disposed) return;
      setStatus((s) => (s === "idle" ? "connecting" : s));
      try {
        const inv = await api.getWTInvite(code);
        if (disposed) return;
        setInvite(inv);
        const kind = inv.item_kind;
        const id = inv.item_id;
        const match = !guestKind || (kind === guestKind && id === guestId);
        setCanWatchTogether(match);
        if (!match) {
          setError("This room is for a different title.");
          setStatus("ended");
          return;
        }
        const joined = await api.joinWT({ code });
        if (disposed) return;
        const roomId = joined.room_id || inv.room_id || inv.id || "";
        setRoom({ id: roomId, code, item_kind: joined.item_kind ?? kind, item_id: joined.item_id ?? id, title: inv.title });
        const ticket = await api.wtTicket(roomId);
        if (disposed) return;
        const path = ticket.ws_url || ticket.url;
        if (!path) throw new Error("the watch party server did not return a connection address");
        setMemberId(ticket.member_id ?? "");
        const ws = new WebSocket(wsUrl(path));
        wsRef.current = ws;
        ws.onopen = () => {
          attempt = 0;
          lastReady = null;
          setError(null);
          for (let i = 0; i < FAST_PINGS; i++) window.setTimeout(ping, i * 250);
          pingTimer = window.setInterval(ping, PING_MS);
          reportTimer = window.setInterval(report, REPORT_MS);
        };
        ws.onmessage = (ev) => {
          let msg: Record<string, unknown>;
          try {
            msg = JSON.parse(String(ev.data)) as Record<string, unknown>;
          } catch {
            return;
          }
          switch (msg.type) {
            case "pong":
              clock.add(Number(msg.t0), Number(msg.server_ms), Date.now());
              break;
            case "state":
              setStatus("synced");
              applyState(msg as unknown as PartyState);
              break;
            case "presence":
              setMembers((msg.members as PartyMember[]) ?? []);
              setHostId(String(msg.host ?? ""));
              if (msg.sync) setSyncInfo(msg.sync as PartySyncInfo);
              break;
            case "sync": {
              const c = parseCorrection(msg, clock.serverNow());
              if (!c) break;
              setLastCorrection(c);
              onCorrectionRef.current?.(c);
              break;
            }
            case "kicked":
              disposed = true;
              setStatus("ended");
              setError(msg.code === "share_revoked" ? "The share link for this party was revoked." : "You are no longer in this party.");
              break;
          }
        };
        ws.onclose = () => {
          stopTimers();
          if (wsRef.current === ws) wsRef.current = null;
          scheduleReconnect();
        };
      } catch (err) {
        if (disposed) return;
        setError(err instanceof Error ? err.message : "watch party connection failed");
        scheduleReconnect();
      }
    };

    void connect();
    return () => {
      disposed = true;
      window.clearTimeout(retryTimer);
      stopTimers();
      const ws = wsRef.current;
      wsRef.current = null;
      ws?.close();
      clock.reset();
      setStatus("idle");
    };
  }, [code, guestKind, guestId, send]);

  const createRoom = useCallback(async (itemKind: string, itemId: string) => {
    const created = await api.createWTRoom({ item_kind: itemKind, item_id: itemId });
    const next = created.code || created.invite_code || created.id;
    setRoom({ ...created, code: next });
    setCanWatchTogether(true);
    return { ...created, code: next };
  }, []);

  const control = useCallback(
    (action: "play" | "pause" | "seek", positionMs: number) => send({ type: action, position_ms: Math.max(0, Math.floor(positionMs)) }),
    [send],
  );

  const setShared = useCallback((on: boolean) => send({ type: "settings", shared_control: on }), [send]);

  const isHost = Boolean(memberId) && memberId === hostId;
  const peers: WTPeer[] = members.map((m) => ({ id: m.id, name: m.display_name, host: m.host }));

  return {
    invite,
    room,
    peers,
    members,
    memberId,
    isHost,
    canControl: isHost || sharedControl,
    sharedControl,
    syncInfo,
    sync,
    status,
    lastCorrection,
    error,
    canWatchTogether,
    createRoom,
    send,
    control,
    setSharedControl: setShared,
    sharePath: opts.shareToken
      ? `/s/${opts.shareToken}/together/${opts.code ?? room?.code ?? ""}`
      : `/together/${opts.code ?? room?.code ?? ""}`,
  };
}
