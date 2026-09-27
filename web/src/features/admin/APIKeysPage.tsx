import { FormEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Card, CardGrid, PageHeader, primaryBtn } from "./ui";

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

  const list = keys.data ?? [];

  return (
    <div className="space-y-4">
      <PageHeader
        title="API keys"
        description={
          <>
            Keys for agents and scripts. Send <code className="text-ink">Authorization: Bearer vd_…</code>. The secret is shown once.
          </>
        }
      />
      <CardGrid>
        <Card id="api-keys-create" title="Create key">
          <form onSubmit={onCreate} className="space-y-3">
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
            <button type="submit" className={primaryBtn}>
              Create key
            </button>
          </form>
          {secret ? (
            <p className="break-all rounded-md border border-line bg-overlay px-3 py-2 text-sm">
              Copy this key now: <code>{secret}</code>
            </p>
          ) : null}
        </Card>
        <Card id="api-keys-list" title="Keys">
          {keys.isSuccess && list.length === 0 ? <p className="text-xs text-dim">No keys yet.</p> : null}
          {list.length ? (
            <ul className="divide-y divide-line rounded-md border border-line">
              {list.map((k) => (
                <li key={k.id} className="flex items-center justify-between gap-2 px-3 py-3 text-sm">
                  <div className="min-w-0">
                    <p className="font-medium">{k.name}</p>
                    <p className="break-all text-xs text-dim">
                      {k.prefix}… · {(k.scopes || []).join(", ")}
                      {k.last_used_at ? ` · used ${k.last_used_at}` : " · never used"}
                    </p>
                  </div>
                  <button
                    type="button"
                    className="shrink-0 text-xs text-danger"
                    onClick={() => api.revokeAPIKey(k.id).then(() => qc.invalidateQueries({ queryKey: ["api-keys"] }))}
                  >
                    Revoke
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </Card>
      </CardGrid>
    </div>
  );
}
