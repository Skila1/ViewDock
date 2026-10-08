import { describe, expect, it } from "vitest";
import { AGE_LIMIT_PRESETS, ageLimitLabel, ageLimitOptions } from "./households";

describe("age limit helpers", () => {
  it("keeps the presets when the current value is one of them", () => {
    expect(ageLimitOptions(13)).toBe(AGE_LIMIT_PRESETS);
  });

  it("adds a non-preset value in order so existing limits stay selectable", () => {
    const values = ageLimitOptions(12).map((o) => o.value);
    expect(values).toContain(12);
    expect(values).toEqual([...values].sort((a, b) => a - b));
  });

  it("labels limits", () => {
    expect(ageLimitLabel(0)).toBe("No restriction");
    expect(ageLimitLabel(13)).toBe("Up to 13 (PG-13)");
    expect(ageLimitLabel(12)).toBe("Up to 12");
  });
});
