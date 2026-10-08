export type AbortKind = "fragment" | "playlist" | "other";

/** hls.js abort records often omit "frag" from type/details while still carrying frag metadata. */
export function classifyNetworkAbort(data: {
  type?: string;
  details?: string;
  reason?: string;
  frag?: unknown;
}): AbortKind {
  if (data.frag != null) return "fragment";
  const kind = `${data.type ?? ""} ${data.details ?? ""} ${data.reason ?? ""}`;
  if (/frag|fragment/i.test(kind)) return "fragment";
  if (/level|manifest|playlist/i.test(kind)) return "playlist";
  return "other";
}
