import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";

export function APIKeysPage() {
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["api-keys"], queryFn: api.listAPIKeys });
  const scopes = useQuery({ queryKey: ["api-key-scopes"], queryFn: api.listAPIKeyScopes });
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>(["admin"]);
  const [secret, setSecret] = useState("");
  const [err, setErr] = useState("");

  const toggle = (s: string) => {
    setPicked((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]));
  };

  const onCreate = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    setSecret("");
    try {
      const created = await api.createAPIKey({ name, scopes: picked });
      setSecret(created.secret || "");
      setName("");
      await qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (e) {
      setErr(e instanceof Error ? e.message : "could not create key");
    }
  };

  return (
    <div className="max-w-2xl space-y-5">
      <div>
        <h1 className="text-base font-medium">API keys</h1>
        <p className="text-sm text-dim">
          Create a key for agents and scripts. Use{" "}
          <code className="text-ink">Authorization: Bearer vd_…</code>. The secret is shown once.
        </p>
      </div>
      <form onSubmit={onCreate} className="space-y-3 rounded-md border border-line p-4">
        <label className="block text-xs text-dim">
          Name
          <input className="mt-1 w-full" value={name} onChange={(e) => setName(e.target.value)} placeholder="cursor-debug" required />
        </label>
        <div className="space-y-2">
          {(scopes.data ?? []).map((s) => (
            <label key={s.name} className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={picked.includes(s.name)} onChange={() => toggle(s.name)} />
              <span>
                <span className="font-medium">{s.name}</span>
                <span className="block text-xs text-dim">{s.description}</span>
              </span>
            </label>
          ))}
        </div>
        {err ? <p className="text-xs text-danger">{err}</p> : null}
        <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
          Create key
        </button>
      </form>
      {secret ? (
        <p className="break-all rounded-md border border-line bg-raised px-3 py-2 text-sm">
          Copy this key now: <code>{secret}</code>
        </p>
      ) : null}
      <ul className="divide-y divide-line rounded-md border border-line">
        {(keys.data ?? []).map((k) => (
          <li key={k.id} className="flex items-center justify-between px-3 py-3 text-sm">
            <div>
              <p className="font-medium">{k.name}</p>
              <p className="text-xs text-dim">
                {k.prefix}… · {(k.scopes || []).join(", ")}
                {k.last_used_at ? ` · used ${k.last_used_at}` : " · never used"}
              </p>
            </div>
            <button
              type="button"
              className="text-xs text-danger"
              onClick={() => api.revokeAPIKey(k.id).then(() => qc.invalidateQueries({ queryKey: ["api-keys"] }))}
            >
              Revoke
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
