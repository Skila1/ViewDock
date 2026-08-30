import { describe, expect, it } from "vitest";
import { classifyNetworkAbort } from "./abortClassify";

describe("classifyNetworkAbort", () => {
  it("counts frag metadata as a fragment abort even when type/details omit frag", () => {
    expect(
      classifyNetworkAbort({
        type: "networkError",
        details: "internalException",
        reason: "aborted",
        frag: { sn: 4, start: 258, duration: 6 },
      }),
    ).toBe("fragment");
    expect(
      classifyNetworkAbort({
        type: "networkError",
        details: "aborted",
        frag: { sn: 5 },
      }),
    ).toBe("fragment");
  });

  it("still classifies from type/details when frag is absent", () => {
    expect(classifyNetworkAbort({ type: "networkError", details: "fragLoadError" })).toBe("fragment");
    expect(classifyNetworkAbort({ type: "networkError", details: "levelLoadError" })).toBe("playlist");
    expect(classifyNetworkAbort({ type: "networkError", details: "aborted" })).toBe("other");
  });
});
