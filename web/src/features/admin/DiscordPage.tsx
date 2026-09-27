import { FormEvent, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/api";
import { discordLabsApi, type DiscordChannelLink, type DiscordCheck, type DiscordInteractionsStatus } from "@/api/discordLabs";
import type { ConfigSetting, DiscordSettings } from "@/types/api.gen";
import { cn } from "@/lib/cn";
import { useAuth } from "@/store/auth";
import { DiagnosticsPanel } from "./discord/DiagnosticsPanel";
import { needsAutoRun } from "./discord/diagnostics";

const BOT_TOKEN_KEY = "discord.bot_token";
const PUBLIC_KEY_KEY = "discord.public_key";
const SEPARATE_KEY = "discord.bot.separate";
const SEP_TOKEN_KEY = "discord.bot.separate_token";
const SEP_PUBLIC_KEY_KEY = "discord.bot.separate_public_key";

const DIAG_KEY = ["discord-diagnostics"];

function errText(e: unknown, fallback: string) {
  return e instanceof Error && e.message ? e.message : fallback;
}

type Note = { ok: boolean; text: string } | null;

function NoteLine({ note }: { note: Note }) {
  if (!note) return null;
  return (
    <p role="status" className={note.ok ? "text-xs text-ok" : "text-xs text-danger"}>
      {note.text}
    </p>
  );
}

function Card({
  id,
  title,
  description,
  aside,
  className,
  children,
}: {
  id: string;
  title: string;
  description?: ReactNode;
  aside?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section id={id} aria-labelledby={`${id}-title`} className={cn("min-w-0 scroll-mt-4 space-y-3 rounded-lg border border-line bg-raised p-4", className)}>
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 id={`${id}-title`} className="text-sm font-semibold">
            {title}
          </h2>
          {description ? <p className="mt-0.5 text-xs text-dim">{description}</p> : null}
        </div>
        {aside}
      </header>
      {children}
    </section>
  );
}

function Pill({ tone, children }: { tone: "ok" | "warn" | "dim" | "accent"; children: ReactNode }) {
  const cls = { ok: "text-ok", warn: "text-warn", dim: "text-dim", accent: "text-accent" }[tone];
  return <span className={cn("shrink-0 rounded-full bg-overlay px-2 py-0.5 text-[11px] font-medium", cls)}>{children}</span>;
}

function savedText(s: ConfigSetting | undefined, unset = "not set") {
  if (!s?.set) return unset;
  return s.source === "environment" ? "set from environment" : "saved";
}

const inputCls = "mt-1 w-full";
const primaryBtn = "btn-green rounded-full px-4 py-1.5 text-sm";
const secondaryBtn = "rounded-full border border-line px-4 py-1.5 text-sm";

/* Discord authentication: sign-in, the Superadmin identity and the OAuth application credentials. */
function AuthCard({ data, onSaved }: { data: DiscordSettings | undefined; onSaved: () => Promise<void> }) {
  const [login, setLogin] = useState(false);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [superID, setSuperID] = useState("");
  const [admins, setAdmins] = useState("");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!data) return;
    setLogin(data.login_enabled);
    setClientId(data.client_id);
    setSuperID(data.superadmin_discord_id ?? "");
    setAdmins(data.admin_discord_ids);
  }, [data]);

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    setNote(null);
    if (login && !superID.trim()) {
      setNote({ ok: false, text: "Set your Superadmin Discord user ID before enabling Discord sign-in." });
      return;
    }
    setBusy(true);
    try {
      await api.putDiscordSettings({
        login_enabled: login,
        client_id: clientId,
        client_secret: secret || undefined,
        superadmin_discord_id: superID,
        admin_discord_ids: admins,
      });
      setSecret("");
      setNote({ ok: true, text: "Saved" });
      await onSaved();
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not save Discord settings") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="discord-auth"
      title="Discord authentication"
      description="The Discord application people sign in with. Its client ID and secret always belong to this application."
      aside={data ? <Pill tone={data.login_enabled ? "ok" : "dim"}>{data.login_enabled ? "Sign-in on" : "Sign-in off"}</Pill> : null}
    >
      <form onSubmit={onSave} className="space-y-3">
        <p className="text-xs text-dim">
          When Discord sign-in is on, local username/password login and signup are completely off. Put your Discord user ID below
          and save before you enable it, so the Superadmin account stays yours. Other people can link an existing account under
          Settings → Connected, or join through Discord if registration is allowed.
        </p>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={login} onChange={(e) => setLogin(e.target.checked)} />
          Enable Discord sign-in (turns off all local login and signup)
        </label>
        <label className="block text-xs text-dim">
          Superadmin Discord user ID (required to enable)
          <input className={inputCls} value={superID} onChange={(e) => setSuperID(e.target.value)} placeholder="123456789012345678" />
          <span className="mt-1 block">Discord → Settings → Advanced → Developer Mode, then right-click your profile → Copy User ID.</span>
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Client ID (Application ID)
            <input className={inputCls} value={clientId} onChange={(e) => setClientId(e.target.value)} />
          </label>
          <label className="block text-xs text-dim">
            Client secret {data?.client_secret_set ? "(saved; leave blank to keep)" : "(not set)"}
            <input className={inputCls} type="password" value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" />
          </label>
        </div>
        {data?.redirect_uri ? (
          <label className="block text-xs text-dim">
            Redirect URL (add it under OAuth2, Redirects)
            <input className={inputCls} readOnly value={data.redirect_uri} onFocus={(e) => e.currentTarget.select()} />
          </label>
        ) : null}
        <label className="block text-xs text-dim">
          Extra administrator Discord user IDs (comma-separated, optional)
          <input className={inputCls} value={admins} onChange={(e) => setAdmins(e.target.value)} />
        </label>
        <p className="text-xs text-dim">
          Set the public URL under Admin → Settings so this redirect stays stable behind your reverse proxy or Cloudflare Tunnel.
        </p>
        <NoteLine note={note} />
        <button type="submit" disabled={busy || !data} className={primaryBtn}>
          Save authentication
        </button>
      </form>
    </Card>
  );
}

/* Registration restrictions for new Discord sign-ups. */
function RegistrationCard({ data, onSaved }: { data: DiscordSettings | undefined; onSaved: () => Promise<void> }) {
  const [reg, setReg] = useState(false);
  const [guildOn, setGuildOn] = useState(false);
  const [guildID, setGuildID] = useState("");
  const [roleOn, setRoleOn] = useState(false);
  const [roleID, setRoleID] = useState("");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!data) return;
    setReg(data.registration_enabled);
    setGuildOn(Boolean(data.registration_guild_enabled));
    setGuildID(data.registration_guild_id ?? "");
    setRoleOn(Boolean(data.registration_role_enabled));
    setRoleID(data.registration_role_id ?? "");
  }, [data]);

  const onSave = async (e: FormEvent) => {
    e.preventDefault();
    setNote(null);
    setBusy(true);
    try {
      await api.putDiscordSettings({
        registration_enabled: reg,
        registration_guild_enabled: guildOn,
        registration_guild_id: guildID,
        registration_role_enabled: roleOn,
        registration_role_id: roleID,
      });
      setNote({ ok: true, text: "Saved" });
      await onSaved();
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not save registration settings") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="discord-registration"
      title="Registration restrictions"
      description="New Discord sign-ups must pass these checks. Already-linked accounts and listed administrators skip them."
      aside={data ? <Pill tone={data.registration_enabled ? "ok" : "dim"}>{data.registration_enabled ? "Open" : "Closed"}</Pill> : null}
    >
      <form onSubmit={onSave} className="space-y-3">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={reg} onChange={(e) => setReg(e.target.checked)} />
          Allow new users to register with Discord
        </label>
        <div className="space-y-3 rounded-md border border-line p-3">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={guildOn} onChange={(e) => setGuildOn(e.target.checked)} />
            Require membership in a Discord server
          </label>
          <label className="block text-xs text-dim">
            Guild / server ID
            <input className={inputCls} value={guildID} onChange={(e) => setGuildID(e.target.value)} placeholder="123456789012345678" />
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={roleOn} onChange={(e) => setRoleOn(e.target.checked)} />
            Require a Discord role in that server
          </label>
          <label className="block text-xs text-dim">
            Role ID
            <input className={inputCls} value={roleID} onChange={(e) => setRoleID(e.target.value)} placeholder="123456789012345678" />
          </label>
        </div>
        <p className="text-xs text-dim">
          Membership and roles are checked with the person&apos;s own Discord sign-in, so the bot does not need to be in that server.
        </p>
        <NoteLine note={note} />
        <button type="submit" disabled={busy || !data} className={primaryBtn}>
          Save restrictions
        </button>
      </form>
    </Card>
  );
}

/* Bot configuration: shared with the sign-in application by default, or a separate application. */
function BotCard({ discord, onSaved }: { discord: DiscordSettings | undefined; onSaved: () => Promise<void> }) {
  const cfg = useQuery({ queryKey: ["admin-config"], queryFn: api.getConfig });
  const setting = (key: string) => cfg.data?.settings.find((s) => s.key === key);
  const separateSetting = setting(SEPARATE_KEY);
  const savedSeparate = separateSetting?.value === "1";
  const [separate, setSeparate] = useState(false);
  const [token, setToken] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (cfg.data) setSeparate(savedSeparate);
  }, [cfg.data, savedSeparate]);

  const tokenKey = separate ? SEP_TOKEN_KEY : BOT_TOKEN_KEY;
  const keyKey = separate ? SEP_PUBLIC_KEY_KEY : PUBLIC_KEY_KEY;
  const tokenSetting = setting(tokenKey);
  const keySetting = setting(keyKey);
  const owner = separate ? "separate bot application" : "sign-in application";
  const modeDirty = Boolean(separateSetting) && separate !== savedSeparate;

  const save = async (values: Record<string, string | null>, done: string) => {
    if (!cfg.data) return;
    setNote(null);
    setBusy(true);
    try {
      await api.putConfig({ version: cfg.data.version, values, note: "Discord bot" });
      setToken("");
      setPublicKey("");
      setNote({ ok: true, text: done });
      await cfg.refetch();
      await onSaved();
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the bot settings could not be saved") });
    } finally {
      setBusy(false);
    }
  };

  const onSave = (e: FormEvent) => {
    e.preventDefault();
    const values: Record<string, string | null> = {};
    if (modeDirty) values[SEPARATE_KEY] = separate ? "1" : "0";
    if (token.trim()) values[tokenKey] = token.trim();
    if (publicKey.trim()) values[keyKey] = publicKey.trim();
    if (separate && !tokenSetting?.set && !token.trim()) {
      setNote({ ok: false, text: "Enter the separate bot token before turning on the separate configuration." });
      return;
    }
    if (Object.keys(values).length === 0) {
      setNote({ ok: false, text: "Enter a bot token or public key to save." });
      return;
    }
    void save(values, modeDirty ? (separate ? "Separate bot configuration turned on" : "The bot now uses the sign-in application") : "Bot settings saved");
  };

  const signIn = discord?.client_id ? `client ID ${discord.client_id}` : "no client ID yet";

  return (
    <Card
      id="discord-bot"
      title="Bot configuration"
      description="The official bot posts party invites and answers slash commands. It never automates a user account."
      aside={cfg.data ? <Pill tone="accent">{savedSeparate ? "Separate application" : "Shared application"}</Pill> : null}
    >
      {cfg.isError ? <p className="text-xs text-danger">{errText(cfg.error, "runtime settings could not be loaded")}</p> : null}
      <form onSubmit={onSave} className="space-y-3">
        {separateSetting ? (
          <label className="flex items-start gap-2 rounded-md border border-line p-3 text-sm">
            <input type="checkbox" className="mt-1" checked={separate} onChange={(e) => setSeparate(e.target.checked)} />
            <span>
              Use separate Discord bot configuration
              <span className="mt-0.5 block text-xs text-dim">
                Off (recommended): sign-in, registration, the bot and slash commands share one Discord application, so you only
                need its bot token and public key below. On: the bot uses a different application&apos;s token and public key; the
                sign-in application&apos;s bot credentials stay saved and are used again when you turn this off.
              </span>
            </span>
          </label>
        ) : null}

        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md bg-overlay p-3 text-xs">
          <dt className="text-dim">Sign-in and registration</dt>
          <dd className="break-all">Sign-in application, {signIn}</dd>
          <dt className="text-dim">Bot and slash commands</dt>
          <dd>
            {savedSeparate ? "Separate bot application" : "Sign-in application"}
            {modeDirty ? <span className="text-warn"> (change not saved yet)</span> : null}
          </dd>
          <dt className="text-dim">Bot token</dt>
          <dd>{savedText(tokenSetting)}</dd>
          <dt className="text-dim">Public key</dt>
          <dd>{savedText(keySetting)}</dd>
        </dl>

        <p className="text-xs text-dim">
          The bot token and public key must come from the same application: the one that receives slash commands. Copy the token
          from its Bot page and the public key from its General Information page. Both are stored encrypted.
        </p>
        {cfg.isSuccess && !keySetting ? (
          <p className="text-xs text-warn">
            This server does not define the <code>{keyKey}</code> setting yet, so the public key cannot be saved here.
          </p>
        ) : null}
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Bot token ({owner}) {tokenSetting?.set ? `(${savedText(tokenSetting)}; leave blank to keep)` : "(not set)"}
            <input className={inputCls} type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          </label>
          <label className="block text-xs text-dim">
            Public key ({owner}) {keySetting?.set ? "(saved; leave blank to keep)" : "(not set)"}
            <input
              className={cn(inputCls, "font-mono")}
              autoComplete="off"
              spellCheck={false}
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
              placeholder="64 hexadecimal characters"
            />
          </label>
        </div>
        <NoteLine note={note} />
        <div className="flex flex-wrap gap-2">
          <button type="submit" disabled={busy || !cfg.data} className={primaryBtn}>
            Save bot settings
          </button>
          {!modeDirty && tokenSetting?.set && tokenSetting.source !== "environment" ? (
            <button
              type="button"
              disabled={busy}
              className={secondaryBtn}
              onClick={() => {
                if (window.confirm("Remove the saved bot token? Slash commands and invites stop working until a new token is saved.")) {
                  void save({ [tokenKey]: null }, "Bot token removed");
                }
              }}
            >
              Remove token
            </button>
          ) : null}
        </div>
      </form>
    </Card>
  );
}

const KEY_STATUS: Record<string, string> = {
  ok: "The saved public key matches the application.",
  saved: "The application public key was saved automatically.",
  unset: "Save the application public key under Bot configuration before setting the endpoint.",
  mismatch: "The saved public key does not match this bot's application. Interactions will be rejected until it is corrected.",
  unknown: "Discord did not report the application public key; check it manually.",
};

/* Register commands and set the endpoint; shared by the slash commands card and the diagnostics panel. */
function useCommandActions(onDone: () => Promise<void>) {
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);

  const register = async (guildID?: string) => {
    setNote(null);
    setBusy(true);
    try {
      const out = await discordLabsApi.registerDiscordCommands({ guild_id: guildID?.trim() || undefined });
      const scope = out.registration.guild_id ? `server ${out.registration.guild_id} (available now)` : "all servers (can take up to an hour to appear)";
      setNote({
        ok: out.public_key_status === "ok" || out.public_key_status === "saved",
        text: `Registered ${out.registration.commands} command for ${scope}. ${KEY_STATUS[out.public_key_status] ?? ""}`,
      });
      await onDone();
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
      await onDone();
    } catch (e) {
      setNote({ ok: false, text: errText(e, "the endpoint could not be set") });
    } finally {
      setBusy(false);
    }
  };

  return { note, busy, register, setEndpoint };
}

type CommandActions = ReturnType<typeof useCommandActions>;

function SlashCommandsCard({ q, actions }: { q: { data?: DiscordInteractionsStatus; isLoading: boolean; isError: boolean; error: unknown }; actions: CommandActions }) {
  const [guildID, setGuildID] = useState("");
  const d = q.data;
  return (
    <Card
      id="discord-commands"
      title="Slash commands"
      description={
        <>
          Members whose Discord account is linked to ViewDock can create a watch party with <code>/party create</code>, post
          invites, check status, link a voice channel, and (host only) pause or resume. Unlinked members are asked to link their
          account first.
        </>
      }
      aside={d ? <Pill tone={d.registration ? "ok" : "warn"}>{d.registration ? "Registered" : "Not registered"}</Pill> : null}
    >
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
              <input className={inputCls} readOnly value={d.endpoint_url} onFocus={(e) => e.currentTarget.select()} />
              <span className="mt-1 block">
                Set it here with the button below, or paste it into Interactions Endpoint URL in the Developer Portal of the{" "}
                {d.mode === "separate" ? "separate bot application" : "sign-in application"}. It must be reachable from the internet.
              </span>
            </label>
          ) : null}
          <label className="block text-xs text-dim">
            Server (guild) ID for instant registration (optional)
            <input className={inputCls} value={guildID} onChange={(e) => setGuildID(e.target.value)} placeholder="Leave blank to register for every server" />
          </label>
          <NoteLine note={actions.note} />
          <div className="flex flex-wrap gap-2">
            <button type="button" disabled={actions.busy || !d.bot_configured} className={primaryBtn} onClick={() => void actions.register(guildID)}>
              Register commands
            </button>
            <button
              type="button"
              disabled={actions.busy || !d.bot_configured || !d.public_key_valid || !d.endpoint_https}
              className={secondaryBtn}
              onClick={() => void actions.setEndpoint()}
            >
              Set endpoint in Discord
            </button>
          </div>
          <p className="text-xs text-dim">Commands: {d.commands.join(", ")}</p>
        </>
      ) : null}
    </Card>
  );
}

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

function PartyInvitesCard({ links, botConfigured, onChanged }: { links: DiscordChannelLink[] | undefined; botConfigured: boolean; onChanged: () => void }) {
  const [channelID, setChannelID] = useState("");
  const [inviteURL, setInviteURL] = useState("");
  const [inviteTitle, setInviteTitle] = useState("ViewDock watch party");
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState(false);

  const sendInvite = async (e: FormEvent) => {
    e.preventDefault();
    setNote(null);
    setBusy(true);
    try {
      await api.sendDiscordBotInvite({ channel_id: channelID, invite_url: inviteURL, title: inviteTitle });
      setNote({ ok: true, text: "Party invite sent" });
    } catch (e2) {
      setNote({ ok: false, text: errText(e2, "Could not send party invite") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card
      id="discord-invites"
      title="Party invitations"
      description="Post a watch party link to a Discord channel. The bot needs View Channel and Send Messages there."
    >
      <form onSubmit={sendInvite} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block text-xs text-dim">
            Discord channel ID
            <input className={inputCls} value={channelID} onChange={(e) => setChannelID(e.target.value)} placeholder="123456789012345678" />
          </label>
          <label className="block text-xs text-dim">
            Message title
            <input className={inputCls} value={inviteTitle} onChange={(e) => setInviteTitle(e.target.value)} placeholder="Message title" />
          </label>
        </div>
        <label className="block text-xs text-dim">
          ViewDock party URL
          <input className={inputCls} value={inviteURL} onChange={(e) => setInviteURL(e.target.value)} placeholder="https://viewdock.example.com/together/…" />
        </label>
        <NoteLine note={note} />
        <button type="submit" disabled={busy || !botConfigured} className={primaryBtn}>
          Send party invite
        </button>
        {!botConfigured ? <p className="text-xs text-warn">Save a bot token under Bot configuration to send invites.</p> : null}
      </form>
      <div className="space-y-2 border-t border-line pt-3">
        <h3 className="text-xs font-medium">Linked channels</h3>
        {!links ? null : links.length === 0 ? (
          <p className="text-xs text-dim">No channels are linked. Links are created by /party create and /party link-voice.</p>
        ) : (
          <ul className="divide-y divide-line rounded-md border border-line">
            {links.map((l) => (
              <LinkRow key={l.channel_id} link={l} onRemoved={onChanged} />
            ))}
          </ul>
        )}
      </div>
    </Card>
  );
}

const FOCUS_TARGETS: Record<string, string> = {
  edit_auth: "discord-auth",
  edit_bot: "discord-bot",
  edit_registration: "discord-registration",
};

export function DiscordPage() {
  const qc = useQueryClient();
  const boot = useAuth((s) => s.boot);
  const settings = useQuery({ queryKey: ["discord-admin"], queryFn: api.getDiscordSettings });
  const interactions = useQuery({ queryKey: ["discord-interactions"], queryFn: discordLabsApi.getDiscordInteractions });
  const diag = useQuery({ queryKey: DIAG_KEY, queryFn: discordLabsApi.getDiscordDiagnostics, refetchInterval: 30_000 });
  const [running, setRunning] = useState(false);
  const [diagNote, setDiagNote] = useState<Note>(null);
  const autoRan = useRef<string | null>(null);

  const runDiagnostics = useCallback(async () => {
    setRunning(true);
    setDiagNote(null);
    try {
      qc.setQueryData(DIAG_KEY, await discordLabsApi.runDiscordDiagnostics());
    } catch (e) {
      setDiagNote({ ok: false, text: errText(e, "Diagnostics could not be run") });
    } finally {
      setRunning(false);
    }
  }, [qc]);

  useEffect(() => {
    const d = diag.data;
    if (!needsAutoRun(d)) return;
    const key = d?.checked_at ?? "never";
    if (autoRan.current === key) return;
    autoRan.current = key;
    void runDiagnostics();
  }, [diag.data, runDiagnostics]);

  const refreshDiscord = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: ["discord-admin"] }),
      qc.invalidateQueries({ queryKey: ["discord-interactions"] }),
      qc.invalidateQueries({ queryKey: DIAG_KEY }),
    ]);
  };

  const onAuthSaved = async () => {
    await refreshDiscord();
    await qc.invalidateQueries({ queryKey: ["system"] });
    await boot();
  };

  const commandActions = useCommandActions(async () => {
    await qc.invalidateQueries({ queryKey: ["discord-interactions"] });
    await qc.invalidateQueries({ queryKey: ["admin-config"] });
    await runDiagnostics();
  });

  const onCheckAction = (c: DiscordCheck) => {
    const a = c.action;
    if (!a) return;
    if (a.id === "register_commands" || a.id === "set_endpoint") {
      setDiagNote(null);
      void (a.id === "register_commands" ? commandActions.register(interactions.data?.registration?.guild_id) : commandActions.setEndpoint());
      return;
    }
    const target = FOCUS_TARGETS[a.id];
    if (target) {
      const el = document.getElementById(target);
      el?.scrollIntoView({ behavior: "smooth", block: "start" });
      el?.querySelector<HTMLElement>("input:not([readonly])")?.focus({ preventScroll: true });
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-base font-medium">Discord</h1>
        <p className="text-sm text-dim">Discord sign-in, registration, the official bot and slash commands.</p>
      </div>
      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_22rem] 2xl:grid-cols-[minmax(0,1fr)_26rem]">
        <aside className="min-w-0 xl:sticky xl:top-4 xl:order-last xl:max-h-[calc(100vh-2rem)] xl:overflow-y-auto">
          <DiagnosticsPanel
            data={diag.data}
            loadError={diag.isError ? errText(diag.error, "diagnostics could not be loaded") : ""}
            running={running}
            notice={diagNote ?? commandActions.note}
            actionBusy={commandActions.busy || running}
            onRun={() => void runDiagnostics()}
            onAction={onCheckAction}
          />
        </aside>
        <div className="grid min-w-0 items-start gap-4 lg:grid-cols-2">
          {settings.isError ? (
            <p className="text-xs text-danger lg:col-span-2">{errText(settings.error, "Discord settings could not be loaded")}</p>
          ) : null}
          <AuthCard data={settings.data} onSaved={onAuthSaved} />
          <BotCard discord={settings.data} onSaved={refreshDiscord} />
          <RegistrationCard data={settings.data} onSaved={onAuthSaved} />
          <SlashCommandsCard q={interactions} actions={commandActions} />
          <PartyInvitesCard
            links={interactions.data?.links}
            botConfigured={Boolean(interactions.data?.bot_configured)}
            onChanged={() => void refreshDiscord()}
          />
        </div>
      </div>
    </div>
  );
}
