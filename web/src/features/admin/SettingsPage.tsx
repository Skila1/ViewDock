import { FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/api/api";
import type { ConfigSetting } from "@/types/api.gen";
import { Card, CardGrid, NoteLine, PageHeader, primaryBtn, type Note } from "./ui";

const SOURCE_LABEL: Record<ConfigSetting["source"], string> = {
  database: "",
  environment: "from environment",
  default: "default",
};

/** Categories edited on their own admin page, so they are not repeated here. */
export const MANAGED_ELSEWHERE: Record<string, { to: string; label: string }> = {
  Discord: { to: "/admin/discord", label: "Discord" },
};

export function settingsGroups(settings: ConfigSetting[]): [string, ConfigSetting[]][] {
  const out = new Map<string, ConfigSetting[]>();
  for (const s of settings) {
    if (MANAGED_ELSEWHERE[s.category]) continue;
    out.set(s.category, [...(out.get(s.category) ?? []), s]);
  }
  return [...out.entries()];
}

function cardId(category: string) {
  return `settings-${category.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;
}

function Field({
  s,
  value,
  reset,
  onChange,
  onReset,
}: {
  s: ConfigSetting;
  value: string;
  reset: boolean;
  onChange: (v: string) => void;
  onReset: () => void;
}) {
  const source = reset ? "will reset" : SOURCE_LABEL[s.source];
  const label = (
    <span className="flex items-center gap-2">
      {s.label}
      {source ? <span className="text-[10px] uppercase tracking-wide text-dim">{source}</span> : null}
      {s.restart ? <span className="text-[10px] uppercase tracking-wide text-warn">restart required</span> : null}
    </span>
  );
  let input;
  if (s.kind === "bool") {
    input = (
      <input type="checkbox" className="mt-1" checked={value === "1"} onChange={(e) => onChange(e.target.checked ? "1" : "0")} />
    );
  } else if (s.kind === "enum") {
    input = (
      <select className="mt-1 w-full" value={value} onChange={(e) => onChange(e.target.value)}>
        {(s.options ?? []).map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    );
  } else {
    input = (
      <input
        className="mt-1 w-full"
        type={s.kind === "secret" ? "password" : s.kind === "int" ? "number" : "text"}
        min={s.kind === "int" ? s.min : undefined}
        max={s.kind === "int" ? s.max : undefined}
        placeholder={s.kind === "secret" ? (s.set ? "Saved. Leave blank to keep" : "Not set") : s.default}
        autoComplete="off"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  return (
    <label className="block text-xs text-dim">
      {label}
      {input}
      {s.help ? <span className="mt-1 block text-[11px] text-dim">{s.help}</span> : null}
      {s.source === "database" && !reset ? (
        <button type="button" className="mt-1 text-[11px] underline" onClick={onReset}>
          {s.kind === "secret" ? "Remove saved value" : "Reset to default"}
        </button>
      ) : null}
    </label>
  );
}

export function SettingsPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin-config"], queryFn: api.getConfig });
  const history = useQuery({ queryKey: ["admin-config-history"], queryFn: api.getConfigHistory });
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [resets, setResets] = useState<Set<string>>(new Set());
  const [msg, setMsg] = useState<Note>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!q.data) return;
    const next: Record<string, string> = {};
    for (const s of q.data.settings) next[s.key] = s.kind === "secret" ? "" : (s.value ?? "");
    setDraft(next);
    setResets(new Set());
  }, [q.data]);

  const groups = useMemo(() => settingsGroups(q.data?.settings ?? []), [q.data]);
  const { hash } = useLocation();

  useEffect(() => {
    if (!hash || !groups.length) return;
    document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView({ block: "start" });
  }, [hash, groups.length]);

  const changes = useMemo(() => {
    const values: Record<string, string | null> = {};
    for (const [, items] of groups) {
      for (const s of items) {
        if (resets.has(s.key)) values[s.key] = null;
        else if (s.kind === "secret") {
          if (draft[s.key]) values[s.key] = draft[s.key];
        } else if ((draft[s.key] ?? "") !== (s.value ?? "")) values[s.key] = draft[s.key] ?? "";
      }
    }
    return values;
  }, [groups, draft, resets]);
  const dirty = Object.keys(changes).length > 0;

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["admin-config"] });
    await qc.invalidateQueries({ queryKey: ["admin-config-history"] });
    await qc.invalidateQueries({ queryKey: ["site-settings"] });
    await qc.invalidateQueries({ queryKey: ["system"] });
  };

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    if (!q.data || !dirty) return;
    setSaving(true);
    setMsg(null);
    try {
      await api.putConfig({ version: q.data.version, values: changes });
      setMsg({ ok: true, text: "Saved. Changes apply immediately on every node." });
      await refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setMsg({ ok: false, text: "Another administrator changed settings. The latest values are loaded; review and save again." });
        await refresh();
      } else {
        setMsg({ ok: false, text: err instanceof Error ? err.message : "Settings could not be saved" });
      }
    } finally {
      setSaving(false);
    }
  };

  const rollback = async (target: number) => {
    if (!q.data) return;
    setMsg(null);
    try {
      await api.rollbackConfig({ target_version: target, version: q.data.version });
      setMsg({ ok: true, text: `Restored the settings from version ${target}.` });
      await refresh();
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : "Rollback failed" });
      await refresh();
    }
  };

  const versions = useMemo(() => {
    const byVersion = new Map<number, { at: string; note?: string; keys: string[] }>();
    for (const h of history.data?.items ?? []) {
      const row = byVersion.get(h.version) ?? { at: h.changed_at, note: h.note, keys: [] };
      row.keys.push(h.reset ? `${h.key} reset` : h.secret ? `${h.key} updated` : `${h.key} = ${h.value}`);
      byVersion.set(h.version, row);
    }
    return [...byVersion.entries()].sort((a, b) => b[0] - a[0]);
  }, [history.data]);

  if (q.isLoading) return <p className="text-sm text-dim">Loading settings…</p>;
  if (q.error) return <p className="text-sm text-danger">Settings could not be loaded.</p>;

  const managed = Object.values(MANAGED_ELSEWHERE);

  return (
    <form onSubmit={onSave} className="space-y-4">
      <PageHeader
        title="Settings"
        description={
          <>
            Changes are validated, versioned and applied without restarting. Secrets are encrypted at rest
            {q.data?.master_key_id ? ` (master key ${q.data.master_key_id})` : ""}.{" "}
            {managed.map((m, i) => (
              <span key={m.to}>
                {i === 0 ? "" : ", "}
                <Link className="text-accent" to={m.to}>
                  {m.label}
                </Link>
              </span>
            ))}{" "}
            settings are on {managed.length === 1 ? "its own page" : "their own pages"}.
          </>
        }
        actions={
          <button type="submit" disabled={saving || !dirty} className={`${primaryBtn} disabled:opacity-50`}>
            {saving ? "Saving…" : dirty ? "Save changes" : "Saved"}
          </button>
        }
      />
      <NoteLine note={msg} />
      <CardGrid>
        {groups.map(([category, items]) => (
          <Card key={category} id={cardId(category)} title={category}>
            <div className="space-y-3">
              {items.map((s) => (
                <Field
                  key={s.key}
                  s={s}
                  value={draft[s.key] ?? ""}
                  reset={resets.has(s.key)}
                  onChange={(v) => {
                    setDraft((d) => ({ ...d, [s.key]: v }));
                    setResets((r) => {
                      const n = new Set(r);
                      n.delete(s.key);
                      return n;
                    });
                  }}
                  onReset={() => setResets((r) => new Set(r).add(s.key))}
                />
              ))}
            </div>
          </Card>
        ))}
        <Card id="settings-history" title="History" description="Every saved version. Restore puts an earlier version back as a new one.">
          {versions.length === 0 ? <p className="text-xs text-dim">No changes recorded yet.</p> : null}
          <ul className="max-h-96 space-y-2 overflow-y-auto">
            {versions.map(([version, row], i) => (
              <li key={version} className="rounded-lg border border-line p-2 text-xs">
                <div className="flex items-center justify-between gap-2">
                  <span>
                    Version {version} · {new Date(row.at).toLocaleString()}
                    {row.note ? ` · ${row.note}` : ""}
                  </span>
                  {i > 0 ? (
                    <button type="button" className="underline" onClick={() => void rollback(version)}>
                      Restore
                    </button>
                  ) : (
                    <span className="text-dim">current</span>
                  )}
                </div>
                <p className="mt-1 break-all text-dim">{row.keys.join(", ")}</p>
              </li>
            ))}
          </ul>
        </Card>
      </CardGrid>
    </form>
  );
}
