import { webcrypto } from "node:crypto";
import { describe, expect, it } from "vitest";
import { CHUNK_OVERHEAD, chunkAad, decryptChunk, encryptChunk, generateVaultKey, sha256Hex } from "./crypto";
import { buildManifest, verifyChunk, verifyManifest } from "./manifest";

const subtle = webcrypto.subtle as unknown as SubtleCrypto;
const bytes = (text: string) => new TextEncoder().encode(text);

describe("chunk encryption", () => {
  it("round trips and adds only iv and tag overhead", async () => {
    const key = await generateVaultKey(subtle);
    const plain = bytes("offline vault chunk");
    const sealed = await encryptChunk(key, plain, chunkAad("u|movie|1", 0), subtle);
    expect(sealed.byteLength).toBe(plain.byteLength + CHUNK_OVERHEAD);
    const opened = await decryptChunk(key, sealed, chunkAad("u|movie|1", 0), subtle);
    expect(new TextDecoder().decode(opened)).toBe("offline vault chunk");
  });

  it("uses a fresh iv per chunk", async () => {
    const key = await generateVaultKey(subtle);
    const a = new Uint8Array(await encryptChunk(key, bytes("same"), chunkAad("k", 0), subtle));
    const b = new Uint8Array(await encryptChunk(key, bytes("same"), chunkAad("k", 0), subtle));
    expect(Buffer.from(a.subarray(0, 12)).equals(Buffer.from(b.subarray(0, 12)))).toBe(false);
  });

  it("rejects tampering, moved chunks, other items and other keys", async () => {
    const key = await generateVaultKey(subtle);
    const sealed = await encryptChunk(key, bytes("payload"), chunkAad("k", 3), subtle);
    const flipped = new Uint8Array(sealed.slice(0));
    flipped[flipped.length - 1] ^= 1;
    await expect(decryptChunk(key, flipped.buffer, chunkAad("k", 3), subtle)).rejects.toThrow();
    await expect(decryptChunk(key, sealed, chunkAad("k", 4), subtle)).rejects.toThrow();
    await expect(decryptChunk(key, sealed, chunkAad("other", 3), subtle)).rejects.toThrow();
    await expect(decryptChunk(await generateVaultKey(subtle), sealed, chunkAad("k", 3), subtle)).rejects.toThrow();
    await expect(decryptChunk(key, new ArrayBuffer(10), chunkAad("k", 3), subtle)).rejects.toThrow(/truncated/);
  });

  it("creates keys that cannot be exported", async () => {
    const key = await generateVaultKey(subtle);
    expect(key.extractable).toBe(false);
    await expect(subtle.exportKey("raw", key)).rejects.toThrow();
  });

  it("hashes with SHA-256", async () => {
    expect(await sha256Hex("abc", subtle)).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  });
});

describe("integrity manifest", () => {
  async function sample() {
    const chunks = [bytes("abcd"), bytes("efgh"), bytes("ij")];
    const hashes = await Promise.all(chunks.map((c) => sha256Hex(c, subtle)));
    const manifest = await buildManifest({ size: 10, chunkSize: 4, mime: "video/mp4", etag: '"x"', chunks: hashes }, subtle);
    return { chunks, manifest };
  }

  it("seals a complete download and verifies it", async () => {
    const { chunks, manifest } = await sample();
    expect(manifest.chunkCount).toBe(3);
    expect(await verifyManifest(manifest, subtle)).toEqual({ ok: true });
    expect(await verifyChunk(manifest, 2, chunks[2].slice().buffer, subtle)).toBe(true);
    expect(await verifyChunk(manifest, 1, bytes("efgX").buffer, subtle)).toBe(false);
    expect(await verifyChunk(manifest, 2, bytes("ijk").buffer, subtle)).toBe(false);
    expect(await verifyChunk(manifest, 3, chunks[2].slice().buffer, subtle)).toBe(false);
  });

  it("refuses to seal an incomplete download", async () => {
    await expect(buildManifest({ size: 10, chunkSize: 4, mime: "video/mp4", chunks: ["a".repeat(64), null, "b".repeat(64)] }, subtle)).rejects.toThrow(/incomplete/);
    await expect(buildManifest({ size: 10, chunkSize: 4, mime: "video/mp4", chunks: ["a".repeat(64)] }, subtle)).rejects.toThrow(/incomplete/);
  });

  it("detects reordered, truncated and altered manifests", async () => {
    const { manifest } = await sample();
    const swapped = { ...manifest, chunks: [manifest.chunks[1], manifest.chunks[0], manifest.chunks[2]] };
    expect(await verifyManifest(swapped, subtle)).toEqual({ ok: false, reason: "root digest mismatch" });
    expect((await verifyManifest({ ...manifest, size: 12 }, subtle)).ok).toBe(false);
    expect((await verifyManifest({ ...manifest, chunks: manifest.chunks.slice(0, 2), chunkCount: 2 }, subtle)).ok).toBe(false);
    expect((await verifyManifest({ ...manifest, chunks: ["zz", ...manifest.chunks.slice(1)] }, subtle)).ok).toBe(false);
    expect((await verifyManifest(null, subtle)).ok).toBe(false);
  });
});
