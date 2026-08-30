export type PlaylistSnap = {
  when: string;
  t: number;
  type: string;
  endlist: boolean;
  mediaSequence: number;
  segmentCount: number;
  sumExtinfSec: number;
  firstSeg: string;
  lastSeg: string;
  headerDurationMs?: number;
  headerType?: string;
};

export function inspectPlaylistBody(text: string, when: string, headers?: Headers): PlaylistSnap {
  let type = "LIVE";
  let endlist = false;
  let mediaSequence = 0;
  let segmentCount = 0;
  let sumExtinfSec = 0;
  let firstSeg = "";
  let lastSeg = "";
  let pendingExtinf = false;
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line.startsWith("#EXT-X-PLAYLIST-TYPE:")) type = line.slice(21).trim();
    else if (line === "#EXT-X-ENDLIST") endlist = true;
    else if (line.startsWith("#EXT-X-MEDIA-SEQUENCE:")) mediaSequence = Number(line.slice(22).trim()) || 0;
    else if (line.startsWith("#EXTINF:")) {
      pendingExtinf = true;
      const rest = line.slice(8).split(",")[0];
      const sec = Number(rest);
      if (Number.isFinite(sec)) sumExtinfSec += sec;
      segmentCount += 1;
    } else if (pendingExtinf && line && !line.startsWith("#")) {
      if (!firstSeg) firstSeg = line;
      lastSeg = line;
      pendingExtinf = false;
    }
  }
  const listed = headers ? Number(headers.get("X-VD-Playlist-Duration-Ms")) : NaN;
  return {
    when,
    t: Date.now(),
    type,
    endlist,
    mediaSequence,
    segmentCount,
    sumExtinfSec: Math.round(sumExtinfSec * 1000) / 1000,
    firstSeg,
    lastSeg,
    headerDurationMs: Number.isFinite(listed) && listed > 0 ? listed : undefined,
    headerType: headers?.get("X-VD-Playlist-Type") || undefined,
  };
}
