import { afterEach, describe, expect, it } from "vitest";
import { bufferedHlsConfig, desktopApp } from "./desktop";

afterEach(() => {
  delete (window as { viewdockDesktop?: unknown }).viewdockDesktop;
});

describe("desktop app", () => {
  it("is absent in a browser", () => {
    expect(desktopApp()).toBeNull();
  });

  it("is read from the bridge the app exposes", () => {
    (window as { viewdockDesktop?: unknown }).viewdockDesktop = { version: "1.0.0", platform: "win32", mediaBufferMB: 1000 };
    expect(desktopApp()?.version).toBe("1.0.0");
  });

  it("keeps a minute ahead in the app and a few seconds in a browser", () => {
    expect(bufferedHlsConfig(null).maxBufferLength).toBe(5);
    const app = bufferedHlsConfig({ version: "1.0.0", platform: "win32", mediaBufferMB: 1000 });
    expect(app.maxBufferLength).toBe(60);
    // Forward bytes plus ten seconds behind stay inside the media buffer.
    expect(app.maxBufferSize).toBeLessThan(1000 * 1000 * 1000);
  });
});
