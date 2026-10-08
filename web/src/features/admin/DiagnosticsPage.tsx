import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { RefreshCw } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getConnectivity, onConnectivity, type Connectivity } from "@/api/client";
import { diagnosticsApi, type ResilienceDashboard, type Section } from "@/api/diagnostics";
import { cn } from "@/lib/cn";
import { errText, FlightEventList, FlightTimeline } from "./diagnostics/FlightTimeline";
import { ReliabilityPanel } from "./diagnostics/ReliabilityPanel";
import { formatAge, formatDuration, ms, statusLabel, statusTone } from "./diagnostics/format";
import { PageHeader } from "./ui";

const REFRESH_MS = 15_000;

function StatusText({ status }: { status?: string }) {
  return <span className={cn("text-[10px] font-medium uppercase tracking-wide", statusTone(status))}>{statusLabel(status)}</span>;
}

function Card({ title, section, children }: { title: string; section?: Section<unknown>; children?: ReactNode }) {
  return (
    <section className="min-w-0 space-y-2 rounded-lg border border-line bg-raised p-4">
      <header className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{title}</h2>
        <StatusText status={section?.status} />
      </header>
      {section?.error ? <p className="text-xs text-danger">{section.error}</p> : null}
      {section?.stale ? <p className="text-xs text-warn">Showing the last known state.</p> : null}
      <div className="space-y-1 text-xs">{children}</div>
      {section?.checked_at ? <p className="text-[10px] text-dim">Checked {formatAge(section.checked_at)}</p> : null}
    </section>
  );
}

function Kv({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex justify-between gap-3">
      <span className="text-dim">{label}</span>
      <span className="truncate text-right">{value}</span>
    </div>
  );
}

function useConnectivity(): Connectivity {
  const [state, setState] = useState<Connectivity>(getConnectivity());
  useEffect(() => onConnectivity(setState), []);
  return state;
}

function FrontendCard() {
  const conn = useConnectivity();
  const [online, setOnline] = useState(typeof navigator === "undefined" ? true : navigator.onLine);
  const [sw, setSw] = useState<"controlled" | "installed" | "none" | "unsupported">("none");
  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    if (!("serviceWorker" in navigator)) setSw("unsupported");
    else if (navigator.serviceWorker.controller) setSw("controlled");
    else void navigator.serviceWorker.getRegistration().then((r) => setSw(r ? "installed" : "none")).catch(() => setSw("none"));
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);
  const status = !online || !conn.api ? "degraded" : "ok";
  const swLabel = { controlled: "active, offline shell available", installed: "installed, activates on reload", none: "not installed", unsupported: "not supported by this browser" }[sw];
  return (
    <Card title="Frontend (this browser)" section={{ status, checked_at: "" }}>
      <Kv label="Network" value={online ? "online" : "offline"} />
      <Kv label="Control API" value={conn.api ? "reachable" : `unreachable for ${formatDuration((Date.now() - conn.since) / 1000)}`} />
      <Kv label="Service worker" value={swLabel} />
      {conn.staleSince ? <Kv label="Cached data age" value={formatAge(new Date(conn.staleSince).toISOString())} /> : null}
    </Card>
  );
}

function ServiceCards({ d }: { d: ResilienceDashboard }) {
  const db = d.database.data;
  const pool = db?.pool;
  const storage = d.storage.data;
  const coord = d.coordinator.data;
  const prog = d.progress.data;
  const flight = d.flight_recorder.data?.stats;
  return (
    <>
      <Card title="Control API" section={d.control}>
        <Kv label="Version" value={d.control.data?.version ?? "n/a"} />
        <Kv label="Role" value={d.control.data?.role ?? "n/a"} />
        <Kv label="Uptime" value={d.control.data ? formatDuration(d.control.data.uptime_seconds) : "n/a"} />
      </Card>
      <Card title="Database" section={d.database}>
        {db ? (
          <>
            <Kv label="Driver" value={db.driver || "n/a"} />
            <Kv label="Health watcher" value={db.watched ? `active, ${db.trips} outages since start` : "not active, checked on demand"} />
            {db.since ? <Kv label={db.available ? "Up since" : "Down since"} value={formatAge(db.since)} /> : null}
            {pool ? <Kv label="Connections" value={`${pool.in_use} in use, ${pool.idle} idle, ${pool.max_open || "unlimited"} max`} /> : null}
            {pool && pool.wait_count > 0 ? <Kv label="Pool waits" value={`${pool.wait_count} (${ms(pool.wait_duration_ms)})`} /> : null}
          </>
        ) : null}
      </Card>
      <Card title="Object storage" section={d.storage}>
        <Kv label="Provider" value={storage?.provider || "none"} />
        {storage?.latency_ms !== undefined ? <Kv label="Write probe" value={`${storage.latency_ms} ms`} /> : null}
        {d.storage.status === "unconfigured" ? <p className="text-dim">No object storage is connected; media is served from local disks.</p> : null}
      </Card>
      <Card title="Party coordinator" section={d.coordinator}>
        {coord ? (
          <>
            <Kv label="Rooms" value={coord.rooms} />
            <Kv label="Participants" value={coord.participants} />
            {Object.entries(coord.details ?? {}).map(([k, v]) => (
              <Kv key={k} label={k.replace(/_/g, " ")} value={typeof v === "object" ? JSON.stringify(v) : String(v)} />
            ))}
          </>
        ) : d.coordinator.status === "unconfigured" ? (
          <p className="text-dim">Coordinator health is not reported by this server.</p>
        ) : null}
      </Card>
      <Card title="Progress checkpoints" section={d.progress}>
        {prog ? (
          <>
            <Kv label="Position reports" value={prog.reports} />
            <Kv label="Database writes" value={prog.writes} />
            <Kv label="Writes per report" value={prog.reports ? prog.writes_per_report.toFixed(3) : "n/a"} />
            <p className="text-dim">Positions stay in memory and are saved on pause, seek, completion, teardown and every 60 s.</p>
          </>
        ) : null}
      </Card>
      <Card title="Flight recorder" section={d.flight_recorder}>
        {flight ? (
          <>
            <Kv label="Sessions retained" value={`${flight.sessions} of ${flight.max_sessions}`} />
            <Kv label="Events recorded" value={flight.recorded} />
            <Kv label="Dropped by rate limit" value={flight.dropped} />
            <Kv label="Sampled out" value={flight.sampled} />
            <Kv label="Retention" value={formatDuration(flight.retention_seconds)} />
          </>
        ) : null}
      </Card>
      {Object.entries(d.extra ?? {}).map(([name, sec]) => (
        <Card key={name} title={name.replace(/_/g, " ")} section={sec}>
          {sec.data && typeof sec.data === "object"
            ? Object.entries(sec.data as Record<string, unknown>).map(([k, v]) => (
                <Kv key={k} label={k.replace(/_/g, " ")} value={typeof v === "object" ? JSON.stringify(v) : String(v)} />
              ))
            : null}
        </Card>
      ))}
    </>
  );
}

function NodesSection({ section }: { section: ResilienceDashboard["nodes"] }) {
  const d = section.data;
  return (
    <section className="space-y-2">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Media mesh nodes</h2>
        <StatusText status={section.status} />
      </header>
      {section.error ? <p className="text-xs text-danger">{section.error}</p> : null}
      {section.status === "unconfigured" || (d && d.total === 0) ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">
          No media nodes are registered; this server handles playback itself. Manage nodes under <Link className="text-accent" to="/admin/nodes">Nodes</Link>.
        </p>
      ) : null}
      {d && d.total > 0 ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm">
          <span className={d.unhealthy > 0 ? "text-danger" : "text-ok"}>{d.healthy} healthy</span>
          <span className="text-dim">
            {" "}
            · {d.unhealthy} unhealthy · {d.draining} draining · {d.disabled} disabled.{" "}
          </span>
          <Link className="text-accent" to="/admin/nodes">
            Manage nodes
          </Link>
        </p>
      ) : null}
    </section>
  );
}

function SessionsSection() {
  const sessions = useQuery({ queryKey: ["resilience", "sessions"], queryFn: diagnosticsApi.flightSessions, refetchInterval: REFRESH_MS });
  const [selected, setSelected] = useState("");
  const list = sessions.data ?? [];
  return (
    <section className="space-y-2">
      <h2 className="text-sm font-semibold">Playback flight recorder</h2>
      {sessions.isLoading ? <p className="text-sm text-dim">Loading sessions…</p> : null}
      {sessions.isError ? <p className="text-sm text-danger">{errText(sessions.error, "Sessions could not be loaded.")}</p> : null}
      {sessions.isSuccess && list.length === 0 ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">No session timelines are retained on this server.</p>
      ) : null}
      {list.length ? (
        <div className="grid gap-3 lg:grid-cols-[18rem_1fr]">
          <ul className="max-h-96 divide-y divide-line overflow-y-auto rounded-md border border-line text-xs">
            {list.map((s) => (
              <li key={s.session_id}>
                <button
                  type="button"
                  className={cn("w-full px-3 py-2 text-left", selected === s.session_id && "bg-raised")}
                  onClick={() => setSelected(s.session_id)}
                >
                  <span className="font-mono">{s.session_id.slice(0, 8)}</span>
                  <span className="text-dim"> · {s.events} events · {formatAge(s.last_at)}</span>
                  {s.notable ? <span className="text-danger"> · {s.notable} incidents</span> : null}
                  <span className="block text-dim">
                    last: {s.last_type.replace(/_/g, " ")}
                    {s.node ? ` · worker ${s.node}` : ""}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <div className="min-w-0 space-y-2">
            {selected ? (
              <>
                <p className="text-xs text-dim">
                  Session <span className="font-mono">{selected}</span> ·{" "}
                  <Link className="text-accent" to={`/admin/streams/${selected}`}>
                    open inspector
                  </Link>
                </p>
                <FlightTimeline sessionId={selected} />
              </>
            ) : (
              <p className="text-sm text-dim">Select a session to see its timeline.</p>
            )}
          </div>
        </div>
      ) : null}
    </section>
  );
}

export function DiagnosticsPage() {
  const qc = useQueryClient();
  const dash = useQuery({ queryKey: ["resilience", "dashboard"], queryFn: diagnosticsApi.resilience, refetchInterval: REFRESH_MS });
  const d = dash.data;
  const failovers = d?.failovers.data?.events ?? [];

  return (
    <section className="space-y-6">
      <PageHeader
        title="Resilience"
        description="Each service is checked separately, so one outage does not hide the others. Live sessions are under Streams."
        actions={
          <button
            type="button"
            className="flex items-center gap-1 rounded-full border border-line px-3 py-1 text-xs"
            disabled={dash.isFetching}
            onClick={() => void qc.invalidateQueries({ queryKey: ["resilience"] })}
          >
            <RefreshCw className={cn("h-3 w-3", dash.isFetching && "animate-spin")} /> Refresh
          </button>
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <FrontendCard />
        {d ? <ServiceCards d={d} /> : null}
      </div>
      {dash.isLoading ? <p className="text-sm text-dim">Loading service health…</p> : null}
      {dash.isError ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-danger">
          {errText(dash.error, "The control API did not answer.")} Server-side sections are unavailable until it responds; the
          browser status above is still current.
        </p>
      ) : null}

      {d ? <NodesSection section={d.nodes} /> : null}

      {d ? (
        <section className="space-y-2">
          <header className="flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">Recent failovers and incidents</h2>
            <StatusText status={d.failovers.status} />
          </header>
          {failovers.length ? (
            <FlightEventList events={failovers} showSession />
          ) : (
            <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">No failovers, pipeline failures or reconnects recorded.</p>
          )}
        </section>
      ) : null}

      {d ? (
        <section className="space-y-2">
          <header className="flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">Source reliability</h2>
            <StatusText status={d.reliability.status} />
          </header>
          {d.reliability.data ? (
            <ReliabilityPanel data={d.reliability.data} />
          ) : (
            <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">Reliability scoring is not enabled on this server.</p>
          )}
        </section>
      ) : null}

      <SessionsSection />
    </section>
  );
}
