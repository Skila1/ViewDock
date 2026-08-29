import { describe, expect, it } from "vitest";
import { canSeekInWindow } from "./seekWindow";

describe("canSeekInWindow", () => {
  it("rejects targets before the session origin", () => {
    expect(canSeekInWindow({ targetMs: 10 * 60_000, originMs: 55 * 60_000, seekableStartSec: 0, seekableEndSec: 70 })).toBe(false);
  });

  it("rejects targets evicted from the back buffer", () => {
    expect(
      canSeekInWindow({
        targetMs: 30_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 120,
      }),
    ).toBe(false);
  });

  it("allows a skip inside the remaining buffer", () => {
    expect(
      canSeekInWindow({
        targetMs: 90_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 120,
      }),
    ).toBe(true);
  });

  it("allows a skip a bit past the frontier", () => {
    expect(
      canSeekInWindow({
        targetMs: 121_000,
        originMs: 0,
        seekableStartSec: 0,
        seekableEndSec: 120,
      }),
    ).toBe(true);
  });

  it("treats an empty seekable range as only the origin", () => {
    expect(canSeekInWindow({ targetMs: 0, originMs: 55 * 60_000 })).toBe(false);
    expect(canSeekInWindow({ targetMs: 55 * 60_000, originMs: 55 * 60_000 })).toBe(true);
  });
});
