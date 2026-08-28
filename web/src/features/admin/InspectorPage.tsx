import type { ReactNode } from "react";
import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";

function Col({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="min-w-0 rounded-md border border-line bg-raised p-3">
      <h2 className="mb-2 text-xs font-medium uppercase tracking-wide text-dim">{title}</h2>
      {children}
    </section>
  );
}

function Kv({ label, value }: { label: string; value: unknown }) {
  const text = value === null ? "null" : value === undefined || value === "" ? "—" : String(value);
  return (
    <div className="mb-1 flex justify-between gap-3 text-xs">
      <span className="text-dim">{label}</span>
      <span className="truncate text-right text-ink">{text}</span>
    </div>
  );
}

export function InspectorPage() {
  const { sessionId = "" } = useParams();
  const q = useQuery({
    queryKey: ["inspector", sessionId],
    queryFn: () => api.adminInspector(sessionId),
    enabled: Boolean(sessionId),
    refetchInterval: 4000,
  });
  const data = q.data;
  const source = data?.source ?? {};
  const client = data?.client ?? {};
  const decision = data?.decision ?? {};
  const reasons = decision.reasons ?? [];

  if (q.isLoading) return <p className="text-sm text-dim">Loading inspector…</p>;
  if (q.isError) return <p className="text-sm text-danger">Inspector unavailable</p>;

  return (
    <div>
      <h1 className="mb-3 text-base font-medium">Inspector</h1>
      <p className="mb-3 font-mono text-xs text-dim">{sessionId}</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Col title="Source">
          <Kv label="path" value={source.path ?? source.filename} />
          <Kv label="container" value={source.container} />
          <Kv label="video" value={source.video_codec} />
          <Kv label="audio" value={source.audio_codec} />
          <Kv label="size" value={source.width && source.height ? `${source.width}×${source.height}` : undefined} />
          <Kv label="hdr" value={source.hdr} />
          <Kv label="gpu" value={source.gpu ?? null} />
        </Col>
        <Col title="Client">
          <Kv label="mse" value={client.mse} />
          <Kv label="hls_native" value={client.hls_native} />
          <Kv label="hevc" value={client.hevc} />
          <Kv label="ac3" value={client.ac3} />
          <Kv label="viewport" value={client.viewport_w ? `${client.viewport_w}×${client.viewport_h}` : undefined} />
          <Kv label="ua" value={client.user_agent} />
        </Col>
        <Col title="Decision">
          <Kv label="mode" value={decision.mode} />
          <Kv label="delivery" value={decision.delivery} />
          <Kv label="quality" value={decision.quality} />
          <Kv label="gpu" value={decision.gpu ?? null} />
          <p className="mt-2 text-[11px] text-dim">Reasons</p>
          <ul className="mt-1 space-y-1 text-xs">
            {reasons.length ? reasons.map((r) => <li key={r}>{r}</li>) : <li className="text-dim">None</li>}
          </ul>
        </Col>
      </div>
    </div>
  );
}
