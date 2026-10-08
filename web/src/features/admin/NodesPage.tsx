import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type BackendNode, type BackendNodeInput, type BackendNodeRole } from "@/api/api";
import { cn } from "@/lib/cn";
import { Card, errText, PageHeader, primaryBtn } from "./ui";

const ROLES: { value: BackendNodeRole; label: string }[] = [
  { value: "media-worker", label: "Media worker (transcode and stream)" },
  { value: "transcode-worker", label: "Transcode only" },
  { value: "storage-worker", label: "Storage only" },
];

const EMPTY: BackendNodeInput = {
  name: "",
  host: "",
  port: 8080,
  scheme: "https",
  role: "media-worker",
  region: "",
  capabilities: "",
  priority: 10,
  weight: 1,
  capacity: 0,
  enabled: true,
  draining: false,
};

type WorkerHealth = {
  version?: string;
  sessions?: number;
  transcode_slots?: number;
  transcode_active?: number;
  hw_available?: boolean;
};

function parseHealth(raw: string): WorkerHealth | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw);
    return v && typeof v === "object" ? (v as WorkerHealth) : null;
  } catch {
    return null;
  }
}

function toInput(n: BackendNode): BackendNodeInput {
  return {
    id: n.id,
    name: n.name,
    host: n.host,
    port: n.port,
    scheme: n.scheme,
    role: n.role,
    region: n.region,
    capabilities: n.capabilities,
    priority: n.priority,
    weight: n.weight,
    capacity: n.capacity,
    enabled: n.enabled,
    draining: n.draining,
  };
}

function StatusBadge({ n }: { n: BackendNode }) {
  let label = n.status || "unknown";
  let tone = "text-dim";
  if (!n.enabled) label = "disabled";
  else if (n.draining) {
    label = "draining";
    tone = "text-warn";
  } else if (n.status === "healthy") tone = "text-ok";
  else if (n.status === "unhealthy") tone = "text-danger";
  return <span className={cn("text-[10px] uppercase tracking-wide", tone)}>{label}</span>;
}

function NodeForm({ initial, onDone }: { initial: BackendNodeInput; onDone: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<BackendNodeInput>(initial);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof BackendNodeInput>(k: K, v: BackendNodeInput[K]) => setF((cur) => ({ ...cur, [k]: v }));
  const num = (v: string) => (v === "" ? 0 : Number(v));

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    setBusy(true);
    try {
      await api.saveNode({ ...f, name: f.name.trim(), host: f.host.trim(), region: f.region.trim() });
      await qc.invalidateQueries({ queryKey: ["admin-nodes"] });
      onDone();
    } catch (e) {
      setErr(errText(e, "the node could not be saved"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block text-xs text-dim">
          Name
          <input className="mt-1 w-full" value={f.name} onChange={(e) => set("name", e.target.value)} required />
        </label>
        <label className="block text-xs text-dim">
          Role
          <select className="mt-1 w-full" value={f.role} onChange={(e) => set("role", e.target.value as BackendNodeRole)}>
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <label className="block text-xs text-dim">
          Scheme
          <select className="mt-1 w-full" value={f.scheme} onChange={(e) => set("scheme", e.target.value as "http" | "https")}>
            <option value="https">https</option>
            <option value="http">http (trusted LAN or same host only)</option>
          </select>
        </label>
        <label className="block text-xs text-dim">
          Host or IP
          <input className="mt-1 w-full" value={f.host} onChange={(e) => set("host", e.target.value)} placeholder="worker-1.lan" required />
        </label>
        <label className="block text-xs text-dim">
          Port
          <input className="mt-1 w-full" type="number" min={1} max={65535} value={f.port} onChange={(e) => set("port", num(e.target.value))} required />
        </label>
        <label className="block text-xs text-dim">
          Region
          <input className="mt-1 w-full" value={f.region} onChange={(e) => set("region", e.target.value)} placeholder="home" />
        </label>
        <label className="block text-xs text-dim">
          Priority
          <input className="mt-1 w-full" type="number" value={f.priority} onChange={(e) => set("priority", num(e.target.value))} />
          <span className="mt-1 block text-[11px]">Higher tiers serve first; lower tiers are standby.</span>
        </label>
        <label className="block text-xs text-dim">
          Weight
          <input className="mt-1 w-full" type="number" min={0} value={f.weight} onChange={(e) => set("weight", num(e.target.value))} />
          <span className="mt-1 block text-[11px]">Share of new sessions within the same priority tier.</span>
        </label>
        <label className="block text-xs text-dim">
          Capacity
          <input className="mt-1 w-full" type="number" min={0} value={f.capacity} onChange={(e) => set("capacity", num(e.target.value))} />
          <span className="mt-1 block text-[11px]">Informational stream capacity; 0 means unspecified.</span>
        </label>
        <label className="block text-xs text-dim">
          Capabilities
          <input className="mt-1 w-full" value={f.capabilities} onChange={(e) => set("capabilities", e.target.value)} placeholder="nvenc,hevc" />
        </label>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={f.enabled} onChange={(e) => set("enabled", e.target.checked)} />
        Enabled
      </label>
      {err ? <p className="text-xs text-danger">{err}</p> : null}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="btn-green rounded-full px-4 py-1.5 text-sm">
          {f.id ? "Save node" : "Add node"}
        </button>
        <button type="button" className="rounded-full border border-line px-4 py-1.5 text-sm" onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function NodeRow({ n, onEdit }: { n: BackendNode; onEdit: () => void }) {
  const qc = useQueryClient();
  const [err, setErr] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const health = parseHealth(n.health);

  const run = async (fn: () => Promise<unknown>, fallback: string) => {
    setErr("");
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      setErr(errText(e, fallback));
    } finally {
      setBusy(false);
      await qc.invalidateQueries({ queryKey: ["admin-nodes"] });
    }
  };

  const issue = () =>
    run(async () => {
      if (n.has_credential && !window.confirm(`Rotate the credential for ${n.name}? The worker stops accepting requests until it is restarted with the new secret.`)) return;
      const out = await api.issueNodeCredential(n.id);
      setSecret(out.secret);
    }, "the credential could not be issued");

  return (
    <li className="space-y-2 px-3 py-3 text-sm">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="flex items-center gap-2 font-medium">
            {n.name}
            <StatusBadge n={n} />
          </p>
          <p className="break-all text-xs text-dim">
            {n.scheme}://{n.host}:{n.port} · {n.role} · priority {n.priority} · weight {n.weight}
            {n.region ? ` · ${n.region}` : ""}
          </p>
          <p className="text-xs text-dim">
            {n.status === "healthy" ? `${n.latency_ms} ms` : "no recent heartbeat"}
            {health?.version ? ` · v${health.version}` : ""}
            {health && typeof health.sessions === "number" ? ` · ${health.sessions} sessions` : ""}
            {health && typeof health.transcode_slots === "number"
              ? ` · transcodes ${health.transcode_active ?? 0}/${health.transcode_slots}`
              : ""}
            {health?.hw_available ? " · GPU" : ""}
            {n.has_credential ? " · credential issued" : " · no credential"}
          </p>
          {n.failures > 0 || n.last_error ? (
            <p className="text-xs text-danger">
              {n.failures > 0 ? `${n.failures} consecutive failures` : "last failure"}
              {n.last_failure_at ? ` · ${new Date(n.last_failure_at).toLocaleString()}` : ""}
              {n.last_error ? ` · ${n.last_error}` : ""}
            </p>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-3 text-xs">
          <button type="button" disabled={busy} onClick={() => run(() => api.probeNode(n.id), "probe failed")}>
            Probe
          </button>
          <button type="button" disabled={busy} onClick={() => run(() => api.drainNode(n.id, !n.draining), "could not change draining")}>
            {n.draining ? "Resume" : "Drain"}
          </button>
          <button type="button" disabled={busy} onClick={() => void issue()}>
            {n.has_credential ? "Rotate credential" : "Issue credential"}
          </button>
          <button type="button" disabled={busy} onClick={onEdit}>
            Edit
          </button>
          <button
            type="button"
            className="text-danger"
            disabled={busy}
            onClick={() => {
              if (!window.confirm(`Remove ${n.name}? Sessions on it are recreated elsewhere.`)) return;
              void run(() => api.deleteNode(n.id), "the node could not be removed");
            }}
          >
            Remove
          </button>
        </div>
      </div>
      {secret ? (
        <p className="break-all rounded-md border border-line bg-raised px-3 py-2 text-xs">
          Set <code>VD_NODE_SECRET</code> on this worker and restart it. Shown once: <code>{secret}</code>
        </p>
      ) : null}
      {err ? <p className="text-xs text-danger">{err}</p> : null}
    </li>
  );
}

export function NodesPage() {
  const nodes = useQuery({ queryKey: ["admin-nodes"], queryFn: api.listNodes, refetchInterval: 10_000 });
  const [editing, setEditing] = useState<BackendNodeInput | null>(null);
  const list = [...(nodes.data ?? [])].sort((a, b) => b.priority - a.priority || a.name.localeCompare(b.name));

  return (
    <div className="space-y-4">
      <PageHeader
        title="Media nodes"
        description={
          <>
            Workers that transcode and stream. Playback is placed on the healthiest node in the highest priority tier and fails over
            automatically. Run each worker with <code className="text-ink">VD_ROLE=worker</code> and the credential issued here.
          </>
        }
        actions={
          !editing ? (
            <button type="button" className={primaryBtn} onClick={() => setEditing(EMPTY)}>
              Add node
            </button>
          ) : null
        }
      />
      {editing ? (
        <Card id="nodes-edit" title={editing.id ? "Edit node" : "Add node"}>
          <NodeForm key={editing.id ?? "new"} initial={editing} onDone={() => setEditing(null)} />
        </Card>
      ) : null}
      <Card id="nodes-list" title="Registered nodes">
      {nodes.isLoading ? <p className="text-sm text-dim">Loading nodes…</p> : null}
      {nodes.isError ? <p className="text-sm text-danger">{errText(nodes.error, "nodes could not be loaded")}</p> : null}
      {nodes.isSuccess && list.length === 0 ? (
        <p className="rounded-md border border-line px-3 py-3 text-sm text-dim">
          No nodes registered. This server handles all playback itself.
        </p>
      ) : null}
      {list.length ? (
        <ul className="divide-y divide-line rounded-md border border-line">
          {list.map((n) => (
            <NodeRow key={n.id} n={n} onEdit={() => setEditing(toInput(n))} />
          ))}
        </ul>
      ) : null}
      </Card>
    </div>
  );
}
