const VIDEO_EXT = new Set([
  ".mkv",
  ".mp4",
  ".avi",
  ".m4v",
  ".mov",
  ".ts",
  ".m2ts",
  ".wmv",
  ".webm",
  ".mpg",
  ".mpeg",
  ".flv",
]);

const BLOCKED = new Set(["thumbs.db", "desktop.ini", ".ds_store"]);

export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024 * 1024;
export const UPLOAD_CHUNK = 8 * 1024 * 1024;
export const VIDEO_ACCEPT = [...VIDEO_EXT].join(",") + ",video/*";

export function isHiddenDropName(name: string): boolean {
  const base = name.replace(/\\/g, "/").split("/").pop() ?? name;
  if (!base || base.startsWith(".")) return true;
  return BLOCKED.has(base.toLowerCase());
}

export function isVideoFilename(name: string): boolean {
  if (name.includes("..") || name.includes("/") || name.includes("\\")) return false;
  const base = name;
  if (isHiddenDropName(base)) return false;
  const dot = base.lastIndexOf(".");
  if (dot < 0) return false;
  return VIDEO_EXT.has(base.slice(dot).toLowerCase());
}

export function pickUploadFiles(list: FileList | File[]): File[] {
  return Array.from(list).filter((f) => isVideoFilename(f.name) && !isHiddenDropName(f.name));
}
