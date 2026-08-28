import { describe, expect, it } from "vitest";
import { asArray } from "./asArray";

describe("asArray", () => {
  it("passes arrays through", () => {
    expect(asArray([1, 2])).toEqual([1, 2]);
  });

  it("unwraps common collection keys", () => {
    expect(asArray({ movies: [{ id: "1" }] })).toEqual([{ id: "1" }]);
    expect(asArray({ items: ["a"] })).toEqual(["a"]);
  });

  it("returns empty for junk", () => {
    expect(asArray(null)).toEqual([]);
    expect(asArray("x")).toEqual([]);
  });
});
