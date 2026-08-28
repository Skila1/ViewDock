import { FormEvent, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";

export function DiscordPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["discord-admin"], queryFn: api.getDiscordSettings });
  const [login, setLogin] = useState(false);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [reg, setReg] = useState(false);
  const [admins, setAdmins] = useState("");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!q.data) return;
    setLogin(q.data.login_enabled);
    setClientId(q.data.client_id);
    setReg(q.data.registration_enabled);
    setAdmins(q.data.admin_discord_ids);
  }, [q.data]);

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    setMsg("");
    await api.putDiscordSettings({
      login_enabled: login,
      client_id: clientId,
      client_secret: secret || undefined,
      registration_enabled: reg,
      admin_discord_ids: admins,
    });
    setSecret("");
    setMsg("Saved");
    await qc.invalidateQueries({ queryKey: ["discord-admin"] });
  };

  return (
    <form onSubmit={onSave} className="max-w-xl space-y-4">
      <div>
        <h1 className="text-base font-medium">Discord</h1>
        <p className="text-sm text-dim">
          Optional OAuth sign-in. Leave this off if you only want username and password.
          Create an application at the Discord developer portal and set the redirect URL below.
        </p>
      </div>
      {q.data?.redirect_uri ? (
        <label className="block text-xs text-dim">
          Redirect URL
          <input className="mt-1 w-full" readOnly value={q.data.redirect_uri} />
        </label>
      ) : null}
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={login} onChange={(e) => setLogin(e.target.checked)} />
        Enable Discord sign-in
      </label>
      <label className="block text-xs text-dim">
        Client ID
        <input className="mt-1 w-full" value={clientId} onChange={(e) => setClientId(e.target.value)} />
      </label>
      <label className="block text-xs text-dim">
        Client secret {q.data?.client_secret_set ? "(saved — leave blank to keep)" : ""}
        <input
          className="mt-1 w-full"
          type="password"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          autoComplete="off"
        />
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={reg} onChange={(e) => setReg(e.target.checked)} />
        Allow new users to register with Discord
      </label>
      <label className="block text-xs text-dim">
        Administrator Discord user IDs (comma-separated snowflakes)
        <input className="mt-1 w-full" value={admins} onChange={(e) => setAdmins(e.target.value)} />
      </label>
      <p className="text-xs text-dim">
        Set the public URL under Admin → Settings so this redirect stays stable behind your reverse proxy
        or Cloudflare Tunnel.
      </p>
      {msg ? <p className="text-xs text-accent">{msg}</p> : null}
      <button type="submit" className="btn-green rounded-full px-4 py-1.5 text-sm">
        Save
      </button>
    </form>
  );
}
