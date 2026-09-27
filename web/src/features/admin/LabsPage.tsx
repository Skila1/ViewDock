import { FormEvent, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import {
  discordLabsApi,
  type LabsCapabilities,
  type LabsConfig,
  type LabsConfigInput,
  type LabsHealth,
  type LabsMode,
  type LabsStatus,
} from "@/api/discordLabs";
import { cn } from "@/lib/cn";

const MODES: { value: LabsMode; label: string }[] = [
  { value: "rtmp", label: "RTMP / RTMPS receiver (for example OBS or a media server)" },
  { value: "srt", label: "SRT receiver" },
  { value: "v4l2", label: "Virtual camera device (Linux v4l2loopback)" },
];

const SIZES = ["640x360", "854x480", "1280x720", "1920x1080"];

function errText(e: unknown, fallback: string) {
  return e instanceof Error && e.message ? e.message : fallback;
}

function formatMS(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${sec}` : `${m}:${sec}`;
}

const STATE_TONE: Record<string, string> = {
  running: "text-ok",
  starting: "text-warn",
  backoff: "text-warn",
  stopping: "text-warn",
  failed: "text-danger",
};

function NoticeSection({ status, onChange }: { status: LabsStatus; onChange: () => Promise<void> }) {
  const [accepted, setAccepted] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const ack = status.acknowledgment;

  const run = async (fn: () => Promise<unknown>, fallback: string) => {
    setErr("");
    setBusy(true);
    try {
      await fn();
      await onChange();
    } catch (e) {
      setErr(errText(e, fallback));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="space-y-3 rounded-md border border-line p-4">
      <h2 className="text-sm font-medium">Risk notice</h2>
      <div className="max-h-64 overflow-y-auto whitespace-pre-line rounded-md bg-raised p-3 text-xs leading-relaxed">{status.notice.text}</div>
      {ack ? (
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
          <p className="text-dim">
            Acknowledged by {ack.user_id} on {new Date(ack.at).toLocaleString()} (notice {ack.notice_version}).
          </p>
          <button
            type="button"
            disabled={busy}
            className="text-danger"
            onClick={() => {
              if (window.confirm("Disable the broadcaster? A running broadcast stops and the notice must be accepted again.")) {
                void run(discordLabsApi.withdrawLabs, "the module could not be disabled");
              }
            }}
          >
            Disable and withdraw acknowledgment
          </button>
        </div>
      ) : (
        <div className="space-y-3">
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-1" checked={accepted} onChange={(e) => setAccepted(e.target.checked)} />
            I have read this notice and Discord&apos;s current rules, and I will only broadcast media I have the rights to share.
          </label>
          <button
            type="button"
            disabled={!accepted || busy}
            className="btn-green rounded-full px-4 py-1.5 text-sm"
            onClick={() => void run(() => discordLabsApi.acknowledgeLabs(status.notice.version), "the acknowledgment could not be saved")}
          >
            Enable experimental broadcaster
          </button>
        </div>
      )}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </section>
  );
}

function CapabilitiesSection({ caps, onRefresh, refreshing }: { caps: LabsCapabilities; onRefresh: () => void; refreshing: boolean }) {
  return (
    <section className="space-y-2 rounded-md border border-line p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-medium">This server</h2>
        <button type="button" className="text-xs underline" disabled={refreshing} onClick={onRefresh}>
          {refreshing ? "Checking…" : "Check again"}
        </button>
      </div>
      <p className="text-xs text-dim">
        {caps.platform} · {caps.ffmpeg ? `FFmpeg ${caps.ffmpeg_version ?? ""}` : "FFmpeg not found"}
        {caps.platform === "linux" ? ` · v4l2loopback ${caps.v4l2loopback ? "loaded" : "not loaded"}` : ""} · checked{" "}
        {new Date(caps.checked_at).toLocaleTimeString()}
      </p>
      <ul className="space-y-1 text-xs">
        {MODES.map((m) => {
          const st = caps.modes[m.value];
          return (
            <li key={m.value}>
              <span className={st?.available ? "text-ok" : "text-danger"}>{st?.available ? "Available" : "Unavailable"}</span>
              {": "}
              {m.label}
              {!st?.available && st?.reason ? <span className="block text-dim">{st.reason}</span> : null}
            </li>
          );
        })}
      </ul>
      {caps.devices.length ? (
        <p className="text-xs text-dim">
          Video devices: {caps.devices.map((d) => `${d.path}${d.name ? ` (${d.name})` : ""}${d.virtual ? " virtual" : ""}`).join(", ")}
        </p>
      ) : null}
    </section>
  );
}

function toInput(c: LabsConfig): LabsConfigInput {
  return {
    mode: c.mode,
    device: c.device ?? "",
    width: c.width,
    height: c.height,
    fps: c.fps,
    video_kbps: c.video_kbps,
    audio_kbps: c.audio_kbps,
    threads: c.threads,
    selection: { ...c.selection },
  };
}

function ConfigSection({ status, onSaved }: { status: LabsStatus; onSaved: () => Promise<void> }) {
  const [f, setF] = useState<LabsConfigInput>(() => toInput(status.config));
  const [output, setOutput] = useState("");
  const [clearOutput, setClearOutput] = useState(false);
  const [sourceKind, setSourceKind] = useState<"party" | "title">(status.config.selection.item_id ? "title" : "party");
  const [search, setSearch] = useState("");
  const [titleLabel, setTitleLabel] = useState(status.config.selection.item_id ? `${status.config.selection.item_kind} ${status.config.selection.item_id}` : "");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const parties = useQuery({ queryKey: ["labs-parties"], queryFn: discordLabsApi.listLabsParties, refetchInterval: 15_000 });
  const results = useQuery({
    queryKey: ["labs-title-search", search],
    queryFn: () => api.smartSearch(search),
    enabled: sourceKind === "title" && search.trim().length >= 2,
  });
  const hits = (results.data?.items ?? []).filter((h) => h.item_kind === "movie" || h.item_kind === "episode").slice(0, 8);
  const set = <K extends keyof LabsConfigInput>(k: K, v: LabsConfigInput[K]) => setF((cur) => ({ ...cur, [k]: v }));
  const virtualDevices = status.capabilities.devices.filter((d) => d.virtual);
  const network = f.mode !== "v4l2";

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setMsg(null);
    setBusy(true);
    const body: LabsConfigInput = { ...f, device: f.mode === "v4l2" ? f.device : "" };
    if (network && output.trim()) body.output_url = output.trim();
    else if (network && clearOutput) body.output_url = "";
    try {
      const out = await discordLabsApi.putLabsConfig(body);
      setOutput("");
      setClearOutput(false);
      setMsg({ ok: true, text: out.restart_required ? "Saved. Stop and start the broadcaster to apply the changes." : "Saved." });
      await onSaved();
    } catch (err) {
      setMsg({ ok: false, text: errText(err, "the configuration could not be saved") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="space-y-3 rounded-md border border-line p-4">
      <h2 className="text-sm font-medium">Output and source</h2>
      {status.config_error ? <p className="text-xs text-danger">{status.config_error}</p> : null}
      <label className="block text-xs text-dim">
        Output
        <select className="mt-1 w-full" value={f.mode} onChange={(e) => set("mode", e.target.value as LabsMode)}>
          {MODES.map((m) => (
            <option key={m.value} value={m.value}>
              {m.label}
              {status.capabilities.modes[m.value]?.available ? "" : " (unavailable here)"}
            </option>
          ))}
        </select>
      </label>
      {f.mode === "v4l2" ? (
        <label className="block text-xs text-dim">
          Virtual camera device
          <input className="mt-1 w-full" list="labs-devices" value={f.device ?? ""} onChange={(e) => set("device", e.target.value)} placeholder="/dev/video10" required />
          <datalist id="labs-devices">
            {virtualDevices.map((d) => (
              <option key={d.path} value={d.path}>
                {d.name}
              </option>
            ))}
          </datalist>
          <span className="mt-1 block">Video only. Route audio separately, for example through a PulseAudio virtual sink.</span>
        </label>
      ) : (
        <div className="space-y-1">
          <label className="block text-xs text-dim">
            Output URL{" "}
            {status.config.output_url_set ? `(saved: ${status.config.output_display ?? "hidden"}; leave blank to keep)` : "(not set)"}
            <input
              className="mt-1 w-full"
              type="password"
              autoComplete="off"
              value={output}
              onChange={(e) => setOutput(e.target.value)}
              placeholder={f.mode === "srt" ? "srt://192.168.1.20:9000?streamid=viewdock" : "rtmp://127.0.0.1:1935/live/viewdock"}
            />
          </label>
          {status.config.output_url_set ? (
            <label className="flex items-center gap-2 text-xs text-dim">
              <input type="checkbox" checked={clearOutput} onChange={(e) => setClearOutput(e.target.checked)} />
              Remove the saved output URL
            </label>
          ) : null}
          <p className="text-[11px] text-dim">
            Stored encrypted and never shown again. Link-local, cloud metadata, broadcast and multicast addresses are refused. On
            Windows or macOS, send to OBS (or another receiver) on the broadcasting computer and start its virtual camera.
          </p>
          {!status.master_key ? <p className="text-xs text-danger">This server has no master key, so an output URL cannot be stored.</p> : null}
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block text-xs text-dim">
          Resolution
          <select
            className="mt-1 w-full"
            value={`${f.width}x${f.height}`}
            onChange={(e) => {
              const [w, h] = e.target.value.split("x").map(Number);
              setF((cur) => ({ ...cur, width: w, height: h }));
            }}
          >
            {SIZES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label className="block text-xs text-dim">
          Frame rate
          <input className="mt-1 w-full" type="number" min={10} max={60} value={f.fps} onChange={(e) => set("fps", Number(e.target.value))} />
        </label>
        {network ? (
          <>
            <label className="block text-xs text-dim">
              Video bitrate (kbps)
              <input className="mt-1 w-full" type="number" min={300} max={20000} value={f.video_kbps} onChange={(e) => set("video_kbps", Number(e.target.value))} />
            </label>
            <label className="block text-xs text-dim">
              Audio bitrate (kbps)
              <input className="mt-1 w-full" type="number" min={64} max={320} value={f.audio_kbps} onChange={(e) => set("audio_kbps", Number(e.target.value))} />
            </label>
          </>
        ) : null}
        <label className="block text-xs text-dim">
          FFmpeg threads
          <input className="mt-1 w-full" type="number" min={1} max={16} value={f.threads} onChange={(e) => set("threads", Number(e.target.value))} />
          <span className="mt-1 block text-[11px]">Caps CPU use so playback keeps priority.</span>
        </label>
      </div>
      <fieldset className="space-y-2">
        <legend className="text-xs text-dim">Source</legend>
        <div className="flex gap-4 text-sm">
          <label className="flex items-center gap-2">
            <input type="radio" checked={sourceKind === "party"} onChange={() => setSourceKind("party")} />
            Follow a watch party
          </label>
          <label className="flex items-center gap-2">
            <input type="radio" checked={sourceKind === "title"} onChange={() => setSourceKind("title")} />
            A single title
          </label>
        </div>
        {sourceKind === "party" ? (
          <label className="block text-xs text-dim">
            Watch party
            <select
              className="mt-1 w-full"
              value={f.selection.room_id ?? ""}
              onChange={(e) => set("selection", e.target.value ? { room_id: e.target.value } : {})}
            >
              <option value="">Choose a party</option>
              {f.selection.room_id && !(parties.data ?? []).some((p) => p.room_id === f.selection.room_id) ? (
                <option value={f.selection.room_id}>Saved party (not active)</option>
              ) : null}
              {(parties.data ?? []).map((p) => (
                <option key={p.room_id} value={p.room_id}>
                  {p.title || p.item_id} · {p.members} watching · {p.playing ? "playing" : "paused"}
                </option>
              ))}
            </select>
            <span className="mt-1 block">The broadcast follows the party&apos;s title, position and pause state.</span>
            {parties.isError ? <span className="mt-1 block text-danger">{errText(parties.error, "parties could not be loaded")}</span> : null}
          </label>
        ) : (
          <div className="space-y-1">
            <label className="block text-xs text-dim">
              Search titles
              <input className="mt-1 w-full" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Movie or episode" />
            </label>
            {hits.length ? (
              <ul className="divide-y divide-line rounded-md border border-line text-xs">
                {hits.map((h) => (
                  <li key={`${h.item_kind}:${h.item_id}`}>
                    <button
                      type="button"
                      className={cn("w-full px-3 py-1.5 text-left", f.selection.item_id === h.item_id && "text-accent")}
                      onClick={() => {
                        set("selection", { item_kind: h.item_kind, item_id: h.item_id });
                        setTitleLabel(`${h.title}${h.year ? ` (${h.year})` : ""}`);
                      }}
                    >
                      {h.title}
                      {h.year ? ` (${h.year})` : ""} · {h.item_kind}
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
            <p className="text-xs text-dim">{f.selection.item_id ? `Selected: ${titleLabel}` : "No title selected."}</p>
          </div>
        )}
      </fieldset>
      {msg ? <p className={msg.ok ? "text-xs text-ok" : "text-xs text-danger"}>{msg.text}</p> : null}
      <button type="submit" disabled={busy} className="btn-green rounded-full px-4 py-1.5 text-sm">
        Save configuration
      </button>
    </form>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div>
      <p className="text-[10px] uppercase tracking-wide text-dim">{label}</p>
      <p className={cn("text-sm", tone)}>{value}</p>
    </div>
  );
}

function AudioMeter({ db }: { db?: number }) {
  if (db === undefined) return <Stat label="Audio level" value="not measured" />;
  const pct = Math.max(0, Math.min(100, ((db + 60) / 60) * 100));
  return (
    <div>
      <p className="text-[10px] uppercase tracking-wide text-dim">Audio level</p>
      <div className="mt-1 h-2 w-full overflow-hidden rounded bg-raised" role="meter" aria-valuemin={-60} aria-valuemax={0} aria-valuenow={db} aria-label="Audio level">
        <div className={cn("h-full", db > -3 ? "bg-danger" : db > -12 ? "bg-warn" : "bg-ok")} style={{ width: `${pct}%` }} />
      </div>
      <p className="text-xs text-dim">{db.toFixed(1)} dBFS</p>
    </div>
  );
}

function HealthSection({ health, canStart, onChange }: { health: LabsHealth; canStart: boolean; onChange: () => Promise<void> }) {
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const active = ["starting", "running", "backoff", "stopping"].includes(health.state);

  const run = async (fn: () => Promise<unknown>, fallback: string) => {
    setErr("");
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      setErr(errText(e, fallback));
    } finally {
      setBusy(false);
      await onChange();
    }
  };

  return (
    <section className="space-y-3 rounded-md border border-line p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          Broadcaster
          <span className={cn("text-[10px] uppercase tracking-wide", STATE_TONE[health.state] ?? "text-dim")}>{health.state}</span>
          {health.stalled ? <span className="text-[10px] uppercase tracking-wide text-warn">stalled</span> : null}
        </h2>
        <div className="flex gap-2">
          {active ? (
            <button type="button" disabled={busy || health.state === "stopping"} className="rounded-full border border-line px-4 py-1.5 text-sm" onClick={() => void run(discordLabsApi.stopLabs, "the broadcaster could not be stopped")}>
              Stop
            </button>
          ) : (
            <button type="button" disabled={busy || !canStart} className="btn-green rounded-full px-4 py-1.5 text-sm" onClick={() => void run(discordLabsApi.startLabs, "the broadcaster could not be started")}>
              Start
            </button>
          )}
        </div>
      </div>
      {err ? <p className="text-xs text-danger">{err}</p> : null}
      <p className="text-xs text-dim">
        Since {new Date(health.since || Date.now()).toLocaleString()}
        {health.output ? ` · ${health.mode} to ${health.output}` : ""}
        {health.source ? ` · ${health.source.title || health.source.item_id || "source"} at ${formatMS(health.source.position_ms)}${health.source.playing ? "" : " (paused)"}` : ""}
      </p>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Frame rate" value={`${health.fps.toFixed(1)}${health.target_fps ? ` / ${health.target_fps}` : ""}`} tone={health.state === "running" && health.target_fps && health.fps < health.target_fps * 0.9 ? "text-warn" : undefined} />
        <Stat label="Resolution" value={health.width ? `${health.width}x${health.height}` : "not running"} />
        <Stat label="Dropped frames" value={String(health.dropped_frames)} tone={health.dropped_frames > 0 ? "text-warn" : undefined} />
        <Stat label="Duplicated frames" value={String(health.duplicate_frames)} />
        <Stat label="Frames sent" value={String(health.frames)} />
        <Stat label="Output time" value={formatMS(health.out_time_ms)} />
        <Stat label="Speed" value={health.speed ?? "n/a"} />
        <Stat label="Bitrate" value={health.bitrate ?? "n/a"} />
        <AudioMeter db={health.audio_level_db} />
        <Stat label="Restarts" value={String(health.restarts)} tone={health.restarts > 0 ? "text-warn" : undefined} />
      </div>
      {health.next_retry_at ? <p className="text-xs text-warn">Retrying at {new Date(health.next_retry_at).toLocaleTimeString()}.</p> : null}
      {health.last_error ? (
        <p className="break-words text-xs text-danger">
          Last error{health.last_error_at ? ` (${new Date(health.last_error_at).toLocaleString()})` : ""}: {health.last_error}
        </p>
      ) : null}
    </section>
  );
}

function PreviewImage({ kind, nonce, label }: { kind: "outgoing" | "raw"; nonce: number; label: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [nonce]);
  return (
    <figure className="space-y-1">
      <figcaption className="text-xs text-dim">{label}</figcaption>
      <div className="flex aspect-video items-center justify-center overflow-hidden rounded-md border border-line bg-raised">
        {failed ? (
          <span className="text-xs text-dim">No frame yet</span>
        ) : (
          <img src={discordLabsApi.labsPreviewURL(kind, nonce)} alt={label} className="h-full w-full object-contain" onError={() => setFailed(true)} />
        )}
      </div>
    </figure>
  );
}

function PreviewSection({ live }: { live: boolean }) {
  const [nonce, setNonce] = useState(() => Date.now());
  useEffect(() => {
    if (!live) return;
    const id = window.setInterval(() => setNonce(Date.now()), 2000);
    return () => window.clearInterval(id);
  }, [live]);
  return (
    <section className="space-y-2 rounded-md border border-line p-4">
      <h2 className="text-sm font-medium">Preview</h2>
      <p className="text-xs text-dim">Snapshots from the broadcaster process every two seconds: the frames actually sent, and the source before scaling.</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <PreviewImage kind="outgoing" nonce={nonce} label="Outgoing camera frames" />
        <PreviewImage kind="raw" nonce={nonce} label="ViewDock source" />
      </div>
    </section>
  );
}

export function LabsPage() {
  const qc = useQueryClient();
  const [refreshing, setRefreshing] = useState(false);
  const status = useQuery({ queryKey: ["labs-vcam"], queryFn: () => discordLabsApi.getLabs() });
  const acknowledged = Boolean(status.data?.acknowledgment);
  const health = useQuery({
    queryKey: ["labs-vcam-health"],
    queryFn: discordLabsApi.getLabsHealth,
    enabled: acknowledged,
    refetchInterval: 2000,
  });

  const reload = async () => {
    await qc.invalidateQueries({ queryKey: ["labs-vcam"] });
    await qc.invalidateQueries({ queryKey: ["labs-vcam-health"] });
  };

  const refreshCaps = async () => {
    setRefreshing(true);
    try {
      const fresh = await discordLabsApi.getLabs(true);
      qc.setQueryData(["labs-vcam"], fresh);
    } finally {
      setRefreshing(false);
    }
  };

  const s = status.data;
  const h = health.data ?? s?.health;
  const configured = Boolean(s && (s.config.selection.room_id || s.config.selection.item_id) && (s.config.mode === "v4l2" ? s.config.device : s.config.output_url_set));

  return (
    <div className="max-w-3xl space-y-5">
      <div>
        <h1 className="text-base font-medium">Labs: virtual camera broadcaster</h1>
        <p className="text-sm text-dim">
          Experimental and off by default. Renders a watch party or title in a separate FFmpeg process for a person to share
          from their own Discord desktop session. It is isolated from playback and never automates Discord accounts.
        </p>
      </div>
      {status.isLoading ? <p className="text-sm text-dim">Loading…</p> : null}
      {status.isError ? <p className="text-sm text-danger">{errText(status.error, "Labs status could not be loaded")}</p> : null}
      {s ? (
        <>
          <NoticeSection status={s} onChange={reload} />
          <CapabilitiesSection caps={s.capabilities} onRefresh={() => void refreshCaps()} refreshing={refreshing} />
          {acknowledged ? (
            <>
              <ConfigSection key={s.config.updated_at ?? "new"} status={s} onSaved={reload} />
              {h ? <HealthSection health={h} canStart={configured} onChange={reload} /> : null}
              {!configured ? <p className="text-xs text-dim">Save an output and a source before starting.</p> : null}
              <PreviewSection live={h?.state === "running"} />
            </>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
