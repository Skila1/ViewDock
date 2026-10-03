import { describe, expect, it } from "vitest";
import type { RoleRow, UserRow } from "@/types/api.gen";
import { byRank } from "./RolesPage";
import { isNewUser, joinedAt } from "./UsersPage";

const now = Date.parse("2026-10-10T00:00:00Z");
const user = (over: Partial<UserRow>): UserRow => ({ id: "u", username: "u", display_name: "", is_admin: false, ...over });

describe("new accounts", () => {
  it("are new for a week after registering", () => {
    expect(isNewUser(user({ created_at: "2026-10-05T00:00:00Z" }), false, now)).toBe(true);
    expect(isNewUser(user({ created_at: "2026-10-01T00:00:00Z" }), false, now)).toBe(false);
  });

  it("count from connecting Discord when Discord sign-in replaces registration", () => {
    const u = user({ created_at: "2026-01-01T00:00:00Z", discord_linked_at: "2026-10-08T00:00:00Z" });
    expect(joinedAt(u, true)).toBe("2026-10-08T00:00:00Z");
    expect(isNewUser(u, true, now)).toBe(true);
    expect(isNewUser(u, false, now)).toBe(false);
  });
});

describe("group rank", () => {
  it("puts Superadmin above Administrator and User last", () => {
    const roles: RoleRow[] = [
      { id: "sys-user", name: "User", permissions: ["a", "b"] },
      { id: "sys-administrator", name: "Administrator", permissions: Array(12).fill("p") },
      { id: "custom", name: "Moderator", permissions: ["a", "b", "c", "d"] },
      { id: "sys-superadmin", name: "Superadmin", permissions: Array(13).fill("p") },
    ];
    expect([...roles].sort(byRank).map((r) => r.name)).toEqual(["Superadmin", "Administrator", "Moderator", "User"]);
  });
});
