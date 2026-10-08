import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { Card, CardGrid, errText, NoteLine, PageHeader, type Note } from "./ui";

type GrantRow = { key: string; name: string; canDownload: boolean; target: { user_id?: string; role_id?: string } };

function GrantList({
  id,
  title,
  rows,
  options,
  addLabel,
  onSet,
  onRemove,
}: {
  id: string;
  title: string;
  rows: GrantRow[];
  options: { id: string; name: string }[];
  addLabel: string;
  onSet: (target: GrantRow["target"], canDownload: boolean) => void;
  onRemove: (target: GrantRow["target"]) => void;
}) {
  const granted = new Set(rows.map((r) => r.key));
  return (
    <Card id={id} title={title}>
      {rows.length === 0 ? <p className="text-xs text-dim">Nobody has access through this list.</p> : null}
      {rows.length ? (
        <ul className="divide-y divide-line rounded-md border border-line text-sm">
          {rows.map((g) => (
            <li key={g.key} className="flex items-center justify-between gap-2 px-3 py-2">
              <span className="min-w-0 truncate">{g.name}</span>
              <span className="flex shrink-0 items-center gap-3 text-xs">
                <label className="flex items-center gap-1">
                  <input type="checkbox" checked={g.canDownload} onChange={() => onSet(g.target, !g.canDownload)} />
                  Downloads
                </label>
                <button type="button" className="text-danger" onClick={() => onRemove(g.target)}>
                  Remove
                </button>
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      <select
        className="w-full text-xs"
        value=""
        onChange={(e) => {
          const v = e.target.value;
          if (v) onSet(id === "grants-users" ? { user_id: v } : { role_id: v }, false);
        }}
      >
        <option value="">{addLabel}</option>
        {options
          .filter((o) => !granted.has(o.id))
          .map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
      </select>
    </Card>
  );
}

export function GrantsPage() {
  const qc = useQueryClient();
  const libs = useQuery({ queryKey: ["libraries"], queryFn: api.listLibraries });
  const users = useQuery({ queryKey: ["users"], queryFn: api.listUsers });
  const roles = useQuery({ queryKey: ["roles"], queryFn: api.listRoles });
  const [libId, setLibId] = useState("");
  const [note, setNote] = useState<Note>(null);
  const grants = useQuery({
    queryKey: ["lib-grants", libId],
    queryFn: () => api.listLibraryGrants(libId),
    enabled: Boolean(libId),
  });

  const run = async (fn: () => Promise<unknown>, fallback: string) => {
    setNote(null);
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: ["lib-grants", libId] });
      await qc.invalidateQueries({ queryKey: ["user"] });
    } catch (e) {
      setNote({ ok: false, text: errText(e, fallback) });
    }
  };
  const set = (target: GrantRow["target"], canDownload: boolean) =>
    void run(() => api.setLibraryGrant(libId, { ...target, can_download: canDownload }), "Could not save access");
  const remove = (target: GrantRow["target"]) => void run(() => api.deleteLibraryGrant(libId, target), "Could not remove access");

  const userRows: GrantRow[] = (grants.data?.users ?? []).map((g) => ({
    key: g.user_id,
    name: g.display_name || g.username,
    canDownload: Boolean(g.can_download),
    target: { user_id: g.user_id },
  }));
  const roleRows: GrantRow[] = (grants.data?.roles ?? []).map((g) => ({
    key: g.role_id,
    name: g.name,
    canDownload: Boolean(g.can_download),
    target: { role_id: g.role_id },
  }));

  return (
    <div className="space-y-4">
      <PageHeader
        title="Library access"
        description="Admins see every library. Everyone else needs access here, directly or through a group."
        actions={
          <select className="min-w-56" value={libId} onChange={(e) => setLibId(e.target.value)} aria-label="Library">
            <option value="">Select a library…</option>
            {(libs.data ?? []).map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        }
      />
      <NoteLine note={note} />
      {!libId ? <p className="text-sm text-dim">Choose a library to see and change who can open it.</p> : null}
      {libId && grants.isError ? <p className="text-sm text-danger">{errText(grants.error, "Access could not be loaded")}</p> : null}
      {libId && grants.isSuccess ? (
        <CardGrid>
          <GrantList
            id="grants-users"
            title="People"
            rows={userRows}
            options={(users.data ?? []).map((u) => ({ id: u.id, name: u.display_name || u.username }))}
            addLabel="Give a person access…"
            onSet={set}
            onRemove={remove}
          />
          <GrantList
            id="grants-groups"
            title="Groups"
            rows={roleRows}
            options={(roles.data ?? []).map((r) => ({ id: r.id, name: r.name }))}
            addLabel="Give a group access…"
            onSet={set}
            onRemove={remove}
          />
        </CardGrid>
      ) : null}
    </div>
  );
}
