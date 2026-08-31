import { FormEvent, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { useAuth } from "@/store/auth";

export function DiscordPage() {
  const qc = useQueryClient();
  const boot = useAuth((s) => s.boot);
  const q = useQuery({ queryKey: ["discord-admin"], queryFn: api.getDiscordSettings });
  const [login, setLogin] = useState(false);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [reg, setReg] = useState(false);
  const [superID, setSuperID] = useState("");
  const [admins, setAdmins] = useState("");
  const [guildOn, setGuildOn] = useState(false);
  const [guildID, setGuildID] = useState("");
  const [roleOn, setRoleOn] = useState(false);
  const [roleID, setRoleID] = useState("");
  const [msg, setMsg] = useState("");

  useEffect(() => {
    if (!q.data) return;
    setLogin(q.data.login_enabled);
    setClientId(q.data.client_id);
    setReg(q.data.registration_enabled);
    setSuperID(q.data.superadmin_discord_id ?? "");
    setAdmins(q.data.admin_discord_ids);
    setGuildOn(Boolean(q.data.registration_guild_enabled));
    setGuildID(q.data.registration_guild_id ?? "");
    setRoleOn(Boolean(q.data.registration_role_enabled));
    setRoleID(q.data.registration_role_id ?? "");
  }, [q.data]);

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    setMsg("");
    if (login && !superID.trim()) {
      setMsg("Set your Superadmin Discord user ID before enabling Discord sign-in.");
      return;
    }
    try {
      await api.putDiscordSettings({
        login_enabled: login,
        client_id: clientId,
        client_secret: secret || undefined,
        registration_enabled: reg,
        superadmin_discord_id: superID,
        admin_discord_ids: admins,
        registration_guild_enabled: guildOn,
        registration_guild_id: guildID,
        registration_role_enabled: roleOn,
        registration_role_id: roleID,
      });
      setSecret("");
      setMsg("Saved");
      await qc.invalidateQueries({ queryKey: ["discord-admin"] });
      await qc.invalidateQueries({ queryKey: ["system"] });
      await boot();
    } catch (e2) {
      setMsg(e2 instanceof Error ? e2.message : "Could not save Discord settings");
    }
  };

  return (
    <form onSubmit={onSave} className="max-w-xl space-y-4">
      <div>
        <h1 className="text-base font-medium">Discord</h1>
        <p className="text-sm text-dim">
          When Discord sign-in is on, local username/password login and signup are completely off.
          Put your Discord user ID below and save before you enable it, so the Superadmin account
          stays yours. Other people can link an existing account under Settings → Connected, or
          join through Discord if registration is allowed.
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
        Enable Discord sign-in (turns off all local login and signup)
      </label>
      <label className="block text-xs text-dim">
        Superadmin Discord user ID (required to enable)
        <input
          className="mt-1 w-full"
          value={superID}
          onChange={(e) => setSuperID(e.target.value)}
          placeholder="123456789012345678"
        />
        <span className="mt-1 block">Discord → Settings → Advanced → Developer Mode, then right-click your profile → Copy User ID.</span>
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
      <div className="space-y-2 rounded-md border border-line p-3">
        <h2 className="text-sm font-medium">Registration whitelist</h2>
        <p className="text-xs text-dim">New Discord sign-ups must pass these checks. Already-linked accounts skip them.</p>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={guildOn} onChange={(e) => setGuildOn(e.target.checked)} />
          Require membership in a Discord server
        </label>
        <label className="block text-xs text-dim">
          Guild / server ID
          <input className="mt-1 w-full" value={guildID} onChange={(e) => setGuildID(e.target.value)} placeholder="123456789012345678" />
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={roleOn} onChange={(e) => setRoleOn(e.target.checked)} />
          Require a Discord role in that server
        </label>
        <label className="block text-xs text-dim">
          Role ID
          <input className="mt-1 w-full" value={roleID} onChange={(e) => setRoleID(e.target.value)} placeholder="123456789012345678" />
        </label>
      </div>
      <label className="block text-xs text-dim">
        Extra administrator Discord user IDs (comma-separated, optional)
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
