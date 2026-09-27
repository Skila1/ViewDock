import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Card, CardGrid, errText, NoteLine, PageHeader, Pill, type Note } from "@/features/admin/ui";

function Stat({ label, value, to }: { label: string; value: string | number; to: string }) {
  return (
    <Link to={to} className="rounded-md border border-line p-3 hover:border-accent/40">
      <div className="text-xs text-dim">{label}</div>
      <div className="mt-1 text-xl font-semibold">{value}</div>
    </Link>
  );
}

export function AdminPage() {
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const streams = useQuery({ queryKey: ["streams"], queryFn: api.adminStreams, refetchInterval: 15_000 });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const nodes = useQuery({ queryKey: ["admin-nodes"], queryFn: api.listNodes });
  const updates = useQuery({ queryKey: ["admin-updates"], queryFn: api.getUpdates });
  const [note, setNote] = useState<Note>(null);

  const u = updates.data;
  const updateAvailable = Boolean(u?.available && u.latest_version && u.version && u.latest_version !== u.version);
  const nodeList = nodes.data ?? [];
  const healthyNodes = nodeList.filter((n) => n.status === "healthy").length;

  const scan = async (id: string, name: string) => {
    setNote(null);
    try {
      await api.scanLibrary(id);
      setNote({ ok: true, text: `Scanning ${name}` });
    } catch (e) {
      setNote({ ok: false, text: errText(e, `Could not scan ${name}`) });
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader title="Overview" description="What is happening on this server right now." />
      <CardGrid>
        <Card id="overview-now" title="At a glance">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <Stat label="Live streams" value={streams.data?.length ?? 0} to="/admin/streams" />
            <Stat label="Users" value={users.data?.length ?? 0} to="/admin/users" />
            <Stat label="Libraries" value={libs.data?.length ?? 0} to="/admin/grants" />
            <Stat label="Nodes" value={nodeList.length ? `${healthyNodes}/${nodeList.length}` : "Local"} to="/admin/nodes" />
          </div>
        </Card>
        <Card
          id="overview-updates"
          title="Version"
          aside={
            u ? (
              <Pill tone={u.updating ? "accent" : updateAvailable ? "warn" : "ok"}>
                {u.updating ? "Updating" : updateAvailable ? "Update available" : "Up to date"}
              </Pill>
            ) : null
          }
        >
          <p className="text-sm">
            {u?.version || "Unknown"}
            {updateAvailable ? <span className="text-dim"> (latest {u?.latest_version})</span> : null}
          </p>
          <Link className="text-xs text-accent" to="/admin/updates">
            Open updates
          </Link>
        </Card>
        <Card
          id="overview-libraries"
          title="Libraries"
          description="Scan picks up new files straight away instead of waiting for the next automatic scan."
          className="lg:col-span-2"
          aside={
            <Link className="text-xs text-accent" to="/admin/uploads">
              Upload videos
            </Link>
          }
        >
          {libs.isError ? <p className="text-xs text-danger">{errText(libs.error, "Libraries could not be loaded")}</p> : null}
          {libs.isSuccess && libs.data.length === 0 ? <p className="text-xs text-dim">No libraries yet.</p> : null}
          <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {(libs.data ?? []).map((lib) => (
              <li key={lib.id} className="flex items-center justify-between gap-2 rounded-md border border-line px-3 py-2 text-sm">
                <span className="min-w-0 truncate">
                  {lib.name}
                  <span className="ml-2 text-xs text-dim">{lib.content_type}</span>
                </span>
                <button type="button" className="shrink-0 text-xs text-accent" onClick={() => void scan(lib.id, lib.name)}>
                  Scan
                </button>
              </li>
            ))}
          </ul>
          <NoteLine note={note} />
        </Card>
      </CardGrid>
    </div>
  );
}
