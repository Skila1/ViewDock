export function asArray<T>(value: unknown): T[] {
  if (Array.isArray(value)) return value as T[];
  if (value && typeof value === "object") {
    const rec = value as Record<string, unknown>;
    for (const key of ["items", "movies", "series", "results", "sessions", "users"]) {
      if (Array.isArray(rec[key])) return rec[key] as T[];
    }
  }
  return [];
}

export function asObject<T extends object>(value: unknown, fallback: T): T {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as T;
  }
  return fallback;
}
