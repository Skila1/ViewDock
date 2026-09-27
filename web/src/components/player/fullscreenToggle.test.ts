import { describe, expect, it } from "vitest";
import { shouldExitFullscreen } from "./fullscreenToggle";

describe("shouldExitFullscreen", () => {
  it("exits after a chrome tap even when AVKit never presented", () => {
    expect(
      shouldExitFullscreen({
        documentFs: false,
        nativeFs: false,
        pageFs: true,
        chromeFs: true,
      }),
    ).toBe(true);
  });

  it("exits when only the icon state flipped (previous stuck-minimize bug)", () => {
    expect(
      shouldExitFullscreen({
        documentFs: false,
        nativeFs: false,
        pageFs: false,
        chromeFs: true,
      }),
    ).toBe(true);
  });

  it("enters when nothing is fullscreen", () => {
    expect(
      shouldExitFullscreen({
        documentFs: false,
        nativeFs: false,
        pageFs: false,
        chromeFs: false,
      }),
    ).toBe(false);
  });
});
