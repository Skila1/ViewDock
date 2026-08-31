import type { ReactNode } from "react";
import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/api";
import type { PlaybackStreamAction } from "@/types/api.gen";

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

function StreamBlock({ title, s }: { title: string; s?: PlaybackStreamAction }) {
  const action = s?.action ?? "—";
  const codec = s?.to ? `${s.codec ?? "—"} → ${s.to}` : (s?.codec ?? "—");
  return (
    <div className="mb-3">
      <p className="mb-1 text-[11px] font-medium uppercase tracking-wide text-dim">{title}</p>
      <Kv label="codec" value={codec} />
      <Kv label="action" value={action} />
      <Kv label="reason" value={s?.reason} />
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
  const gpu = data?.gpu ?? null;
  const reasons = decision.reasons ?? [];

  if (q.isLoading) return <p className="text-sm text-dim">Loading inspector…</p>;
  if (q.isError) return <p className="text-sm text-danger">Inspector unavailable</p>;

  return (
    <div>
      <h1 className="mb-3 text-base font-medium">Inspector</h1>
      <p className="mb-3 font-mono text-xs text-dim">{sessionId}</p>
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <Col title="Source">
          <Kv label="container" value={source.container} />
          <Kv label="video" value={source.video_codec} />
          <Kv label="audio" value={source.audio_codec} />
          <Kv label="bit depth" value={source.bit_depth} />
          <Kv label="size" value={source.width && source.height ? `${source.width}×${source.height}` : undefined} />
          <Kv label="hdr" value={source.hdr} />
        </Col>
        <Col title="Client">
          <Kv label="mse" value={client.mse} />
          <Kv label="hls_native" value={client.hls_native} />
          <Kv label="hevc" value={client.hevc} />
          <Kv label="hevc_main10" value={client.hevc_main10} />
          <Kv label="eac3" value={client.eac3} />
          <Kv label="viewport" value={client.viewport_w ? `${client.viewport_w}×${client.viewport_h}` : undefined} />
          <Kv label="ua" value={client.user_agent} />
        </Col>
        <Col title="Decision">
          <Kv label="playback" value={decision.playback} />
          <Kv label="mode" value={decision.mode} />
          <Kv label="delivery" value={decision.delivery} />
          <Kv label="hardware" value={decision.hardware} />
          <Kv label="encoder" value={decision.encoder} />
          <Kv label="encoder_type" value={decision.encoder_type} />
          <StreamBlock title="Container" s={decision.container} />
          <StreamBlock title="Video" s={decision.video} />
          <StreamBlock title="Audio" s={decision.audio} />
          <p className="mt-2 text-[11px] text-dim">Reasons</p>
          <ul className="mt-1 space-y-1 text-xs">
            {reasons.length ? reasons.map((r) => <li key={r}>{r}</li>) : <li className="text-dim">None</li>}
          </ul>
        </Col>
        <Col title="Generation">
          <Kv label="hls_attach" value={data?.hls_attach} />
          <Kv label="vod_ondemand" value={data?.vod_ondemand} />
          <Kv label="vod_plan_kind" value={data?.vod_plan_kind} />
          <Kv label="gen_start_seg" value={data?.gen_start_seg} />
          <Kv label="generation_id" value={data?.generation_id} />
          <Kv label="seekable_from_ms" value={data?.seekable_from_ms} />
          <Kv label="origin_ms" value={data?.origin_ms} />
        </Col>
        <Col title="Hardware">
          <Kv label="available" value={gpu?.available} />
          <Kv label="vendor" value={gpu?.vendor} />
          <Kv label="encoder" value={gpu?.encoder} />
          <Kv label="gpu_used" value={gpu?.gpu_used} />
          <Kv label="fallback" value={gpu?.fallback} />
          <Kv label="fallback_reason" value={gpu?.fallback_reason} />
          <Kv label="detection_reason" value={gpu?.detection_reason} />
        </Col>
      </div>
    </div>
  );
}
