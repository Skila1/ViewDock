import { useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type WatchPartyMember, type WatchPartyRoom } from "@/api/api";
import { formatClock } from "@/lib/format";
import { Card, CardGrid, errText, NoteLine, PageHeader, Pill, secondaryBtn, type Note } from "./ui";

const PANEL_LABEL: Record<WatchPartyRoom["panel"], string> = {
  everyone: "Panel shown to everyone",
  host: "Panel shown to the host only",
  hidden: "Panel hidden",
};

function memberTone(m: WatchPartyMember): { tone: "ok" | "warn" | "dim"; label: string } {
  if (!m.connected) return { tone: "dim", label: "offline" };
  if (m.buffering) return { tone: "warn", label: "buffering" };
  if (!m.ready) return { tone: "dim", label: "loading" };
  return { tone: "ok", label: `${(m.drift_ms / 1000).toFixed(1)} s drift` };
}

function MemberLogs({ member }: { member: WatchPartyMember }) {
  const isUser = member.kind === "user";
  const streams = useQuery({ queryKey: ["streams"], queryFn: api.adminStreams, refetchInterval: 5000 });
  const mine = (streams.data ?? []).filter((s) => isUser && s.user_id === member.id);
  const first = mine[0] ? mine[0].session_id || mine[0].id : "";
  const flight = useQuery({
    queryKey: ["flight", first],
    queryFn: () => api.adminFlightRecorder(first),
    enabled: Boolean(first),
  });
  const logs = useQuery({
    queryKey: ["logs", "actor", member.id],
    queryFn: () => api.listLogs({ actor: member.id, limit: 50 }),
    enabled: isUser,
  });

  return (
    <div className="mt-2 space-y-3 rounded-md border border-line bg-bg p-3 text-xs">
      <section>
        <p className="mb-1 font-medium text-ink">Playback</p>
        {!isUser ? <p className="text-dim">Guests have no account, so their playback is not listed by user.</p> : null}
        {streams.isError ? <p className="text-danger">Live sessions could not be loaded.</p> : null}
        {isUser && streams.isSuccess && mine.length === 0 ? <p className="text-dim">No live playback session.</p> : null}
        {mine.map((s) => {
          const id = s.session_id || s.id;
          return (
            <p key={id} className="text-dim">
              <Link to={`/admin/streams/${id}`} className="text-accent">
                {id.slice(0, 8)}
              </Link>{" "}
              {s.playback || s.mode || "n/a"}, {s.delivery || "n/a"}
              {s.reasons?.length ? ` (${s.reasons.join(", ")})` : ""}
            </p>
          );
        })}
        {flight.data?.length ? (
          <ul className="mt-1 max-h-40 space-y-0.5 overflow-auto font-mono text-[11px] text-dim">
            {flight.data.slice(-30).map((e, i) => (
              <li key={`${e.at}-${i}`}>
                {new Date(e.at).toLocaleTimeString()} {e.type}
                {e.data ? ` ${JSON.stringify(e.data)}` : ""}
              </li>
            ))}
          </ul>
        ) : null}
      </section>
      {isUser ? (
        <section>
          <p className="mb-1 font-medium text-ink">Client logs</p>
          {logs.isLoading ? <p className="text-dim">Loading logs…</p> : null}
          {logs.isError ? <p className="text-danger">{errText(logs.error, "Logs could not be loaded.")}</p> : null}
          {logs.isSuccess && logs.data.items.length === 0 ? <p className="text-dim">No logs from this user.</p> : null}
          {logs.data?.items.length ? (
            <ul className="max-h-60 space-y-0.5 overflow-auto font-mono text-[11px]">
              {logs.data.items.map((l) => (
                <li key={l.id} className={l.level === "error" ? "text-danger" : l.level === "warn" ? "text-warn" : "text-dim"}>
                  {new Date(l.created_at).toLocaleString()} [{l.category}] {l.message}
                  {l.details ? ` ${JSON.stringify(l.details)}` : ""}
                </li>
              ))}
            </ul>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}

function RoomCard({ room }: { room: WatchPartyRoom }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const run = async (fn: () => Promise<unknown>, ok: string, fail: string) => {
    setBusy(true);
    setNote(null);
    try {
      await fn();
      setNote({ ok: true, text: ok });
      await qc.invalidateQueries({ queryKey: ["watch-parties"] });
    } catch (e) {
      setNote({ ok: false, text: errText(e, fail) });
    } finally {
      setBusy(false);
    }
  };

  const endParty = () => {
    if (!window.confirm(`End "${room.title || room.invite_code}" for everyone?`)) return;
    void run(() => api.endWatchParty(room.id), "Party ended.", "The party could not be ended.");
  };

  return (
    <Card
      id={`party-${room.id}`}
      title={room.title || `${room.item_kind} ${room.item_id}`}
      description={
        <>
          {room.discord_channel_id ? "Discord Activity" : "Web"} · code {room.invite_code} · {room.playing ? "playing" : "paused"} at{" "}
          {formatClock(room.position_ms)}
        </>
      }
      aside={
        <button type="button" className={secondaryBtn} disabled={busy} onClick={endParty}>
          End party
        </button>
      }
    >
      <div className="mb-2 flex flex-wrap gap-2">
        <Pill tone="dim">{PANEL_LABEL[room.panel]}</Pill>
        {room.shared_control ? <Pill tone="accent">Shared control</Pill> : null}
        {room.banned ? <Pill tone="warn">{room.banned} blocked</Pill> : null}
      </div>
      {room.members.length === 0 ? <p className="text-xs text-dim">Nobody is in this party right now.</p> : null}
      <ul className="space-y-2">
        {room.members.map((m) => {
          const st = memberTone(m);
          return (
            <li key={m.id} className="border-t border-line pt-2">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="min-w-0 flex-1 truncate">
                  {m.display_name}
                  {m.host ? <span className="ml-1 text-xs text-dim">host</span> : null}
                  {m.owner && !m.host ? <span className="ml-1 text-xs text-dim">owner</span> : null}
                  {m.kind !== "user" ? <span className="ml-1 text-xs text-dim">guest</span> : null}
                </span>
                <Pill tone={st.tone}>{st.label}</Pill>
                <button type="button" className="text-xs text-accent" onClick={() => setOpen(open === m.id ? null : m.id)}>
                  {open === m.id ? "Hide logs" : "Logs"}
                </button>
                <button
                  type="button"
                  className="text-xs text-warn"
                  disabled={busy}
                  onClick={() => void run(() => api.kickWatchPartyMember(room.id, m.id, false), `${m.display_name} was removed.`, "They could not be removed.")}
                >
                  Remove
                </button>
                <button
                  type="button"
                  className="text-xs text-danger"
                  disabled={busy}
                  onClick={() =>
                    void run(
                      () => api.kickWatchPartyMember(room.id, m.id, true),
                      `${m.display_name} was removed and cannot rejoin this party.`,
                      "They could not be removed.",
                    )
                  }
                >
                  Remove and block
                </button>
              </div>
              {open === m.id ? <MemberLogs member={m} /> : null}
            </li>
          );
        })}
      </ul>
      <NoteLine note={note} />
    </Card>
  );
}

export function WatchPartiesPage() {
  const q = useQuery({ queryKey: ["watch-parties"], queryFn: api.adminWatchParties, refetchInterval: 5000 });
  const rooms = q.data ?? [];
  return (
    <div className="space-y-4">
      <PageHeader
        title="Watch parties"
        description="Parties running on the web and in the Discord Activity. Remove people, block them from rejoining, end a party, and read each member's playback and client logs."
      />
      {q.isLoading ? <p className="text-xs text-dim">Loading parties…</p> : null}
      {q.isError ? <p className="text-xs text-danger">{errText(q.error, "Watch parties could not be loaded.")}</p> : null}
      {q.isSuccess && rooms.length === 0 ? <p className="text-sm text-dim">No watch parties are running.</p> : null}
      <CardGrid>
        {rooms.map((room) => (
          <RoomCard key={room.id} room={room} />
        ))}
      </CardGrid>
    </div>
  );
}
