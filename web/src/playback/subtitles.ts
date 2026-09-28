import type { SessionTrack } from "@/types/api.gen";

export type Cue = { startMs: number; endMs: number; text: string };

const TIME = /(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})/;

function parseTime(s: string): number | null {
  const m = TIME.exec(s.trim());
  if (!m) return null;
  const h = Number(m[1] ?? 0);
  const min = Number(m[2]);
  const sec = Number(m[3]);
  const ms = Number(m[4].padEnd(3, "0"));
  return ((h * 60 + min) * 60 + sec) * 1000 + ms;
}

function cleanCueText(lines: string[]): string {
  return lines
    .join("\n")
    .replace(/<\/?(?:c|v|b|u|ruby|rt|lang|font|span)(?:[.\s][^>]*)?>/gi, "")
    .replace(/<\d{1,2}:\d{2}[:.\d]*>/g, "")
    .replace(/\{\\[^}]*\}/g, "")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&nbsp;/g, " ")
    .trim();
}

/** Parses WebVTT (and SRT, which differs only in its header and decimal comma) into sorted cues. */
export function parseCues(text: string): Cue[] {
  const blocks = text.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n").split(/\n{2,}/);
  const cues: Cue[] = [];
  for (const block of blocks) {
    const lines = block.split("\n");
    const at = lines.findIndex((l) => l.includes("-->"));
    if (at < 0) continue;
    const [a, b] = lines[at].split("-->");
    const start = parseTime(a);
    const end = parseTime(b ?? "");
    if (start == null || end == null || end <= start) continue;
    const body = cleanCueText(lines.slice(at + 1));
    if (body) cues.push({ startMs: start, endMs: end, text: body });
  }
  return cues.sort((x, y) => x.startMs - y.startMs);
}

/** Cues showing at a movie position, in start order. */
export function activeCues(cues: Cue[], ms: number): Cue[] {
  let lo = 0;
  let hi = cues.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (cues[mid].startMs <= ms) lo = mid + 1;
    else hi = mid;
  }
  const out: Cue[] = [];
  for (let i = lo - 1; i >= 0 && out.length < 3; i--) {
    if (cues[i].endMs > ms) out.unshift(cues[i]);
    else if (ms - cues[i].startMs > 60_000) break;
  }
  return out;
}

const LANGS: Record<string, string> = {
  eng: "English", en: "English", spa: "Spanish", es: "Spanish", fre: "French", fra: "French", fr: "French",
  ger: "German", deu: "German", de: "German", ita: "Italian", it: "Italian", por: "Portuguese", pt: "Portuguese",
  jpn: "Japanese", ja: "Japanese", kor: "Korean", ko: "Korean", chi: "Chinese", zho: "Chinese", zh: "Chinese",
  rus: "Russian", ru: "Russian", ara: "Arabic", ar: "Arabic", hin: "Hindi", hi: "Hindi", dut: "Dutch", nld: "Dutch",
  nl: "Dutch", swe: "Swedish", sv: "Swedish", nor: "Norwegian", no: "Norwegian", dan: "Danish", da: "Danish",
  fin: "Finnish", fi: "Finnish", pol: "Polish", pl: "Polish", tur: "Turkish", tr: "Turkish", gre: "Greek", ell: "Greek",
  heb: "Hebrew", he: "Hebrew", tha: "Thai", th: "Thai", vie: "Vietnamese", vi: "Vietnamese", ind: "Indonesian", id: "Indonesian",
};

const TEXT_CODECS = new Set(["subrip", "srt", "mov_text", "text", "webvtt", "vtt"]);

/** Image and ASS subtitles cannot be drawn by the web player (it reports ass_js false), so the server burns them in. */
export function subtitleBurnsIn(t: SessionTrack): boolean {
  const codec = typeof t.codec === "string" ? t.codec.toLowerCase() : "";
  return codec !== "" && !TEXT_CODECS.has(codec);
}

/** A readable label for a subtitle track, with SDH and forced flags. */
export function subtitleLabel(t: SessionTrack, n: number): string {
  const lang = typeof t.language === "string" ? t.language.toLowerCase() : "";
  const name = (typeof t.title === "string" && t.title.trim()) || LANGS[lang] || (lang ? lang.toUpperCase() : `Track ${n}`);
  const flags = [t.sdh ? "SDH" : "", t.forced ? "Forced" : ""].filter(Boolean);
  return flags.length && !flags.every((f) => name.toLowerCase().includes(f.toLowerCase())) ? `${name} (${flags.join(", ")})` : name;
}
