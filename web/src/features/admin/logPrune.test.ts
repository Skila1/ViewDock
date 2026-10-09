import { describe, expect, it } from "vitest";
import { pruneCutoff } from "./logPrune";

describe("pruneCutoff", () => {
  it("cuts at the start of the next local day", () => {
    expect(pruneCutoff("2026-10-08")).toBe(new Date(2026, 9, 9).toISOString());
    expect(pruneCutoff("2026-12-31")).toBe(new Date(2027, 0, 1).toISOString());
  });
  it("refuses anything that is not a day", () => {
    expect(pruneCutoff("")).toBeNull();
    expect(pruneCutoff("yesterday")).toBeNull();
  });
});
