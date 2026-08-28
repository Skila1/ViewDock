import { FormEvent, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";

export function SettingsPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["site-settings"], queryFn: api.getSiteSettings });
  const [publicURL, setPublicURL] = useState("");
  const [tmdbKey, setTmdbKey] = useState("");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!q.data) return;
    setPublicURL(q.data.public_url);
  }, [q.data]);

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    setMsg("");
    await api.putSiteSettings({
      public_url: publicURL,
      tmdb_api_key: tmdbKey || undefined,
    });
    setTmdbKey("");
    setMsg("Saved");
    await qc.invalidateQueries({ queryKey: ["site-settings"] });
    await qc.invalidateQueries({ queryKey: ["system"] });
  };

  return (
    <form onSubmit={onSave} className="max-w-xl space-y-4">
      <div>
        <h1 className="text-base font-medium">Settings</h1>
        <p className="text-sm text-dim">
          Public URL is used for Discord OAuth and share links. TMDB is optional metadata.
        </p>
      </div>
      <label className="block text-xs text-dim">
        Public URL
        <input
          className="mt-1 w-full"
          placeholder="https://app.viewdock.dev"
          value={publicURL}
          onChange={(e) => setPublicURL(e.target.value)}
        />
      </label>
      <label className="block text-xs text-dim">
        TMDB API key {q.data?.tmdb_api_key_set ? "(saved — leave blank to keep)" : ""}
        <input
          className="mt-1 w-full"
          type="password"
          value={tmdbKey}
          onChange={(e) => setTmdbKey(e.target.value)}
          autoComplete="off"
        />
      </label>
      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
        Save
      </button>
    </form>
  );
}
