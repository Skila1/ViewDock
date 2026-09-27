import { FormEvent, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { discordLabsApi, type DiscordChannelLink } from "@/api/discordLabs";
import { useAuth } from "@/store/auth";

const BOT_TOKEN_KEY = "discord.bot_token";
const PUBLIC_KEY_KEY = "discord.public_key";

function errText(e: unknown, fallback: string) {
  return e instanceof Error && e.message ? e.message : fallback;
}

type Note = { ok: boolean; text: string } | null;

function NoteLine({ note }: { note: Note }) {
  if (!note) return null;
  return <p className={note.ok ? "text-xs text-ok" : "text-xs text-danger"}>{note.text}</p>;
}

function BotSection() {
  const qc = useQueryClient();
  const cfg = useQuery({ queryKey: ["admin-config"], queryFn: api.getConfig });
  const [token, setToken] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const [channelID, setChannelID] = useState("");
  const [inviteURL, setInviteURL] = useState("");
  const [inviteTitle, setInviteTitle] = useState("ViewDock watch party");
  const [inviteNote, setInviteNote] = useState<Note>(null);

  const setting = (key: string) => cfg.data?.settings.find((s) => s.key === key);
  const tokenSetting = setting(BOT_TOKEN_KEY);
  const keySetting = setting(PUBLIC_KEY_KEY);

  const save = async (values: Record<string, string | null>, done: string) => {
    if (!cfg.data) return;
    setNote(null);
    setBusy(true);
    try {
      await api.putConfig({ version: cfg.data.version, values, note: "Discord bot" });
      setToken("");
      setPublicKey("");
      setNote({ ok: true, text: done });
      await qc.invalidateQueries({ queryKey: ["admin-config"] });
      await qc.invalidateQueries({ queryKey: ["discord-interactions"] });
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the bot settings could not be saved") });
    } finally {
      setBusy(false);
    }
  };

  const onSave = (e: FormEvent) => {
    e.preventDefault();
    const values: Record<string, string | null> = {};
    if (token.trim()) values[BOT_TOKEN_KEY] = token.trim();
    if (publicKey.trim()) values[PUBLIC_KEY_KEY] = publicKey.trim();
    if (Object.keys(values).length === 0) {
      setNote({ ok: false, text: "Enter a bot token or public key to save." });
      return;
    }
    void save(values, "Bot settings saved");
  };

  const sendInvite = async () => {
    setInviteNote(null);
    try {
      await api.sendDiscordBotInvite({ channel_id: channelID, invite_url: inviteURL, title: inviteTitle });
      setInviteNote({ ok: true, text: "Party invite sent" });
    } catch (e) {
      setInviteNote({ ok: false, text: errText(e, "Could not send party invite") });
    }
  };

  return (
    <section className="space-y-3 rounded-md border border-line p-3">
      <div>
        <h2 className="text-sm font-medium">Official bot</h2>
        <p className="text-xs text-dim">
          From your application in the Discord Developer Portal: the bot token (Bot page) and the public key (General
          Information page). Both are stored encrypted. ViewDock uses the official bot API and never automates a user
          account.
        </p>
      </div>
      {cfg.isError ? <p className="text-xs text-danger">{errText(cfg.error, "runtime settings could not be loaded")}</p> : null}
      {cfg.isSuccess && !keySetting ? (
        <p className="text-xs text-warn">
          This server does not define the <code>{PUBLIC_KEY_KEY}</code> setting yet, so the public key cannot be saved here.
        </p>
      ) : null}
      <form onSubmit={onSave} className="space-y-3">
        <label className="block text-xs text-dim">
          Bot token {tokenSetting?.set ? `(saved${tokenSetting.source === "environment" ? " from environment" : ""}; leave blank to keep)` : "(not set)"}
          <input className="mt-1 w-full" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
        </label>
        <label className="block text-xs text-dim">
          Application public key {keySetting?.set ? "(saved; leave blank to keep)" : "(not set)"}
          <input
            className="mt-1 w-full font-mono"
            autoComplete="off"
            spellCheck={false}
            value={publicKey}
            onChange={(e) => setPublicKey(e.target.value)}
            placeholder="64 hexadecimal characters"
          />
        </label>
        <NoteLine note={note} />
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={busy || !cfg.data} className="btn-green rounded-full px-4 py-1.5 text-sm">
            Save bot settings
          </button>
          {tokenSetting?.set && tokenSetting.source !== "environment" ? (
            <button
              type="button"
              disabled={busy}
              className="rounded-full border border-line px-4 py-1.5 text-sm"
              onClick={() => {
                if (window.confirm("Remove the saved bot token? Slash commands and invites stop working until a new token is saved.")) {
                  void save({ [BOT_TOKEN_KEY]: null }, "Bot token removed");
                }
              }}
            >
              Remove token
            </button>
          ) : null}
        </div>
      </form>
      <div className="space-y-2 border-t border-line pt-3">
        <h3 className="text-xs font-medium">Post a party invite</h3>
        <input className="w-full" value={channelID} onChange={(e) => setChannelID(e.target.value)} placeholder="Discord channel ID" />
        <input className="w-full" value={inviteURL} onChange={(e) => setInviteURL(e.target.value)} placeholder="ViewDock party URL" />
        <input className="w-full" value={inviteTitle} onChange={(e) => setInviteTitle(e.target.value)} placeholder="Message title" />
        <NoteLine note={inviteNote} />
        <button type="button" className="btn-green rounded-full px-4 py-1.5 text-sm" onClick={() => void sendInvite()}>
          Send party invite
        </button>
      </div>
    </section>
  );
}

const KEY_STATUS: Record<string, string> = {
  ok: "The saved public key matches the application.",
  saved: "The application public key was saved automatically.",
  unset: "Save the application public key above before setting the endpoint.",
  mismatch: "The saved public key does not match this bot's application. Interactions will be rejected until it is corrected.",
  unknown: "Discord did not report the application public key; check it manually.",
};

function LinkRow({ link, onRemoved }: { link: DiscordChannelLink; onRemoved: () => void }) {
  const [err, setErr] = useState("");
  return (
    <li className="flex flex-wrap items-start justify-between gap-2 px-3 py-2 text-xs">
      <div className="min-w-0">
        <p className="font-medium">
          {link.title || "Untitled party"}{" "}
          <span className={link.room_active ? "text-ok" : "text-dim"}>{link.room_active ? "active" : "ended"}</span>
        </p>
        <p className="break-all text-dim">
          {link.kind === "voice" ? "Voice" : "Text"} channel {link.channel_id} · server {link.guild_id || "unknown"} · invite{" "}
          {link.invite_code} · {new Date(link.updated_at).toLocaleString()}
        </p>
        {err ? <p className="text-danger">{err}</p> : null}
      </div>
      <button
        type="button"
        className="text-danger"
        onClick={async () => {
          setErr("");
          try {
            await discordLabsApi.deleteDiscordLink(link.channel_id);
            onRemoved();
          } catch (e) {
            setErr(errText(e, "the link could not be removed"));
          }
        }}
      >
        Unlink
      </button>
    </li>
  );
}

function InteractionsSection() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["discord-interactions"], queryFn: discordLabsApi.getDiscordInteractions });
  const [guildID, setGuildID] = useState("");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);
  const d = q.data;

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["discord-interactions"] });
    await qc.invalidateQueries({ queryKey: ["admin-config"] });
  };

  const register = async () => {
    setNote(null);
    setBusy(true);
    try {
      const out = await discordLabsApi.registerDiscordCommands({ guild_id: guildID.trim() || undefined });
      const scope = out.registration.guild_id ? `server ${out.registration.guild_id} (available now)` : "all servers (can take up to an hour to appear)";
      setNote({
        ok: out.public_key_status === "ok" || out.public_key_status === "saved",
        text: `Registered ${out.registration.commands} command for ${scope}. ${KEY_STATUS[out.public_key_status] ?? ""}`,
      });
      await refresh();
    } catch (e) {
      setNote({ ok: false, text: errText(e, "commands could not be registered") });
    } finally {
      setBusy(false);
    }
  };

  const setEndpoint = async () => {
    setNote(null);
    setBusy(true);
    try {
      const out = await discordLabsApi.setDiscordEndpoint();
      setNote({ ok: true, text: `Discord verified and saved ${out.endpoint_url} as the interactions endpoint.` });
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the endpoint could not be set") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="space-y-3 rounded-md border border-line p-3">
      <div>
        <h2 className="text-sm font-medium">Slash commands</h2>
        <p className="text-xs text-dim">
          Members whose Discord account is linked to ViewDock can create a watch party with <code>/party create</code>, post
          invites, check status, link a voice channel, and (host only) pause or resume. Unlinked members are asked to link
          their account first.
        </p>
      </div>
      {q.isLoading ? <p className="text-xs text-dim">Loading…</p> : null}
      {q.isError ? <p className="text-xs text-danger">{errText(q.error, "interaction status could not be loaded")}</p> : null}
      {d ? (
        <>
          <ul className="space-y-1 text-xs">
            <li className={d.bot_configured ? "text-ok" : "text-warn"}>{d.bot_configured ? "Bot token saved" : "Bot token not set"}</li>
            <li className={d.public_key_valid ? "text-ok" : "text-warn"}>
              {d.public_key_valid ? "Public key saved" : d.public_key_set ? "Public key is not a valid Ed25519 key" : "Public key not set"}
            </li>
            <li className={d.endpoint_https ? "text-ok" : "text-warn"}>
              {d.endpoint_url
                ? d.endpoint_https
                  ? "Public URL uses https"
                  : "Discord requires an https public URL (Admin, Settings)"
                : "Public URL not set (Admin, Settings)"}
            </li>
            {!d.parties_enabled ? <li className="text-warn">Watch parties are turned off; commands will say so.</li> : null}
            <li className="text-dim">
              {d.registration
                ? `Commands registered ${new Date(d.registration.registered_at).toLocaleString()} for ${d.registration.guild_id ? `server ${d.registration.guild_id}` : "all servers"}`
                : "Commands not registered yet"}
            </li>
          </ul>
          {d.endpoint_url ? (
            <label className="block text-xs text-dim">
              Interactions endpoint URL
              <input className="mt-1 w-full" readOnly value={d.endpoint_url} onFocus={(e) => e.currentTarget.select()} />
              <span className="mt-1 block">
                Set it here with the button below, or paste it into Interactions Endpoint URL in the Developer Portal. It must
                be reachable from the internet.
              </span>
            </label>
          ) : null}
          <label className="block text-xs text-dim">
            Server (guild) ID for instant registration (optional)
            <input className="mt-1 w-full" value={guildID} onChange={(e) => setGuildID(e.target.value)} placeholder="Leave blank to register for every server" />
          </label>
          <NoteLine note={note} />
          <div className="flex flex-wrap gap-2">
            <button type="button" disabled={busy || !d.bot_configured} className="btn-green rounded-full px-4 py-1.5 text-sm" onClick={() => void register()}>
              Register commands
            </button>
            <button
              type="button"
              disabled={busy || !d.bot_configured || !d.public_key_valid || !d.endpoint_https}
              className="rounded-full border border-line px-4 py-1.5 text-sm"
              onClick={() => void setEndpoint()}
            >
              Set endpoint in Discord
            </button>
          </div>
          <p className="text-xs text-dim">Commands: {d.commands.join(", ")}</p>
          <div className="space-y-2">
            <h3 className="text-xs font-medium">Linked channels</h3>
            {d.links.length === 0 ? (
              <p className="text-xs text-dim">No channels are linked. Links are created by /party create and /party link-voice.</p>
            ) : (
              <ul className="divide-y divide-line rounded-md border border-line">
                {d.links.map((l) => (
                  <LinkRow key={l.channel_id} link={l} onRemoved={() => void refresh()} />
                ))}
              </ul>
            )}
          </div>
        </>
      ) : null}
    </section>
  );
}

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
    <div className="max-w-xl space-y-6">
      <form onSubmit={onSave} className="space-y-4">
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
          Client secret {q.data?.client_secret_set ? "(saved; leave blank to keep)" : ""}
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
      <BotSection />
      <InteractionsSection />
    </div>
  );
}
