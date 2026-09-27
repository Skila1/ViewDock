import { describe, expect, it } from "vitest";
import { hardwareAccelerationHelp } from "./hardwareAcceleration";

describe("hardwareAccelerationHelp", () => {
  it("gives Discord steps inside the Activity", () => {
    expect(hardwareAccelerationHelp(true)).toContain("Discord Settings, go to System, turn on Enable Hardware Acceleration");
  });
  it("gives browser steps elsewhere", () => {
    expect(hardwareAccelerationHelp(false)).toContain("Use graphics acceleration when available");
  });
});
