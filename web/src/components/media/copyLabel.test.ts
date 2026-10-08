import { describe, expect, it } from "vitest";
import { copyLabel } from "./TitleMenu";

describe("copyLabel", () => {
  it("names each state of a title's copy on ViewDock", () => {
    expect(copyLabel({ available: true, state: "none", bytes: 0, size: 0 })).toBe("Download to ViewDock");
    expect(copyLabel({ available: true, state: "copying", bytes: 25, size: 100 })).toBe("Downloading to ViewDock (25%)");
    expect(copyLabel({ available: true, state: "copying", bytes: 25, size: 0 })).toBe("Downloading to ViewDock");
    expect(copyLabel({ available: true, state: "done", bytes: 100, size: 100 })).toBe("On ViewDock");
    expect(copyLabel({ available: true, state: "failed", bytes: 0, size: 100, error: "no space" })).toBe("Download to ViewDock (retry)");
  });
});
