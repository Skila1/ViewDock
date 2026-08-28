import type { Me } from "@/types/api.gen";

export function hasPerm(me: Me | null | undefined, name: string): boolean {
  if (!me) return false;
  if (me.is_admin) return true;
  return Boolean(me.permissions?.includes(name) || me.permissions?.includes("admin"));
}
