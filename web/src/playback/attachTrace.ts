export type AttachTraceEvent = {
  t: number;
  ev: string;
  detail?: string;
};

type AttachTrace = {
  events: AttachTraceEvent[];
  engineReason?: string;
  hlsJsSupported?: boolean;
  mmsAvailable?: boolean;
  mseAvailable?: boolean;
  playlistType?: string;
  playlistDurationMs?: number;
  airplayPolicy?: string;
};

const store = new WeakMap<HTMLVideoElement, AttachTrace>();

function state(video: HTMLVideoElement): AttachTrace {
  let cur = store.get(video);
  if (!cur) {
    cur = { events: [] };
    store.set(video, cur);
  }
  return cur;
}

export function noteAttach(video: HTMLVideoElement, ev: string, detail?: string) {
  const cur = state(video);
  cur.events.push({ t: Date.now(), ev, detail });
  if (cur.events.length > 120) cur.events.splice(0, cur.events.length - 120);
}

export function setAttachMeta(video: HTMLVideoElement, patch: Partial<AttachTrace>) {
  Object.assign(state(video), patch);
}

export function readAttachTrace(video: HTMLVideoElement | null): AttachTrace {
  if (!video) return { events: [] };
  const cur = store.get(video);
  return cur ? { ...cur, events: [...cur.events] } : { events: [] };
}
