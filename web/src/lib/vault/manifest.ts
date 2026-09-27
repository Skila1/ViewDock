import { sha256Hex } from "./crypto";
import { chunkCount, chunkLength } from "./range";

// The integrity manifest records the SHA-256 of every plaintext chunk plus a
// root digest over the size, chunk size and ordered chunk digests. WebCrypto
// has no incremental digest, so the root stands in for a whole-file hash: any
// changed, missing, reordered or truncated chunk changes it.

export const MANIFEST_VERSION = 1;

export type VaultManifest = {
  version: typeof MANIFEST_VERSION;
  size: number;
  chunkSize: number;
  chunkCount: number;
  mime: string;
  etag?: string;
  chunks: string[];
  root: string;
};

const HEX64 = /^[0-9a-f]{64}$/;

export function manifestRoot(size: number, chunkSize: number, chunks: readonly string[], subtle?: SubtleCrypto): Promise<string> {
  return sha256Hex(`${MANIFEST_VERSION}:${size}:${chunkSize}:${chunks.join("")}`, subtle);
}

export async function buildManifest(
  input: { size: number; chunkSize: number; mime: string; etag?: string; chunks: readonly (string | null | undefined)[] },
  subtle?: SubtleCrypto,
): Promise<VaultManifest> {
  const expected = chunkCount(input.size, input.chunkSize);
  if (input.chunks.length !== expected || input.chunks.some((hash) => !hash || !HEX64.test(hash))) {
    throw new Error("download is incomplete; every chunk needs a digest before the manifest is sealed");
  }
  const chunks = input.chunks as string[];
  return {
    version: MANIFEST_VERSION,
    size: input.size,
    chunkSize: input.chunkSize,
    chunkCount: expected,
    mime: input.mime,
    etag: input.etag,
    chunks: [...chunks],
    root: await manifestRoot(input.size, input.chunkSize, chunks, subtle),
  };
}

export type ManifestCheck = { ok: true } | { ok: false; reason: string };

/** verifyManifest checks structure and the root digest, not the stored bytes. */
export async function verifyManifest(manifest: VaultManifest | undefined | null, subtle?: SubtleCrypto): Promise<ManifestCheck> {
  if (!manifest) return { ok: false, reason: "missing manifest" };
  if (manifest.version !== MANIFEST_VERSION) return { ok: false, reason: `unsupported manifest version ${String(manifest.version)}` };
  if (!Number.isSafeInteger(manifest.size) || manifest.size <= 0) return { ok: false, reason: "invalid size" };
  if (!Number.isSafeInteger(manifest.chunkSize) || manifest.chunkSize <= 0) return { ok: false, reason: "invalid chunk size" };
  const expected = chunkCount(manifest.size, manifest.chunkSize);
  if (manifest.chunkCount !== expected || manifest.chunks.length !== expected) return { ok: false, reason: "chunk count does not match size" };
  if (manifest.chunks.some((hash) => !HEX64.test(hash))) return { ok: false, reason: "malformed chunk digest" };
  const root = await manifestRoot(manifest.size, manifest.chunkSize, manifest.chunks, subtle);
  if (root !== manifest.root) return { ok: false, reason: "root digest mismatch" };
  return { ok: true };
}

/** verifyChunk checks one decrypted chunk against its manifest entry. */
export async function verifyChunk(manifest: VaultManifest, index: number, plain: ArrayBuffer, subtle?: SubtleCrypto): Promise<boolean> {
  if (index < 0 || index >= manifest.chunkCount) return false;
  if (plain.byteLength !== chunkLength(index, manifest.size, manifest.chunkSize)) return false;
  return (await sha256Hex(plain, subtle)) === manifest.chunks[index];
}
