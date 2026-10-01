# Discord

ViewDock has these Discord features, all configured under **Admin → Discord**:

- **Discord sign-in** lets people sign in with their Discord account, optionally limited to members of your server or a role.
- **The Discord bot** adds a `/party` slash command so people can start and control watch parties from a Discord channel.
- **The Discord Activity** (optional) opens ViewDock inside a voice channel so the channel watches its party together.

Both need a Discord application, created in the [Discord Developer Portal](https://discord.com/developers/applications). Set the public URL under **Admin → Settings** (or `VD_PUBLIC_URL`) first, so the addresses ViewDock gives Discord stay stable behind your reverse proxy or tunnel.

## One application or two

By default sign-in, registration, the bot and slash commands all use one Discord application: the client ID and secret belong to it, and so do the bot token and public key.

To run the bot from a different application, tick **Use separate Discord bot configuration** in the **Bot configuration** card and enter that application's bot token and public key. Sign-in and registration keep using the client ID and secret. The two sets of bot credentials are stored separately: turning the option off switches back to the sign-in application's bot credentials and keeps the separate ones saved for when you turn it on again. Each credential field is labelled with the application it belongs to.

## Status and diagnostics

The **Status and diagnostics** panel (on the right on wide screens, at the top on smaller ones) reports the configuration mode, missing or incomplete credentials, sign-in readiness, the bot's connection to the Discord API, its online presence (the Gateway), its server membership and permissions, slash command registration and the interactions endpoint. Each problem includes a short explanation and, where possible, a button that fixes it or takes you to the right setting, such as adding the bot to a server or registering commands.

The checks against Discord use a short timeout and a handful of requests. They run when you press **Run diagnostics**, and automatically when you open the page if there is no earlier result or the settings changed since the last one; otherwise the page shows the saved result and when it ran. Secrets are never shown in the results.

## Discord sign-in

!!! warning
    When Discord sign-in is on, local username and password sign-in and signup are turned off completely. Save your own Discord user ID as the Superadmin before enabling it, so you keep access to the administrator account.

1. In the Developer Portal, open your application's **OAuth2** page. Copy the **Client ID** and **Client secret**, and add the **Redirect URL** shown on the ViewDock Discord page as a redirect.
2. In Discord, turn on **Settings → Advanced → Developer Mode**, then right-click your profile and choose **Copy User ID**.
3. In **Admin → Discord**, enter the Superadmin Discord user ID, the client ID and the client secret, then save.
4. Optional settings:
    - **Allow new users to register with Discord.** Without it, only existing accounts that have linked Discord can sign in.
    - **Registration whitelist.** Require new sign-ups to be members of a Discord server, and optionally to hold a role in it. Accounts that are already linked skip these checks.
    - **Extra administrator Discord user IDs**, comma-separated.
5. Tick **Enable Discord sign-in** and save.

Existing users can link their Discord account under **Settings → Connected**.

## Discord bot and slash commands

The bot receives slash commands through an HTTPS interactions endpoint on your ViewDock server, so the public URL must use HTTPS and be reachable from the internet.

1. In the Developer Portal, open the **Bot** page and copy the bot token. The application **public key** is on the **General Information** page.
2. Add the bot to your server with the OAuth2 URL generator, selecting the `bot` and `applications.commands` scopes.
3. In **Admin → Discord**, paste the bot token and save. You can paste the public key too; if you leave it empty, ViewDock saves it automatically when you register commands. The page tells you if the saved key does not match the bot's application.
4. Press **Register commands**. Enter a server ID to make the commands available in that server immediately, or leave it blank to register them for every server (global commands can take up to an hour to appear).
5. Press **Set endpoint in Discord** so Discord sends interactions to ViewDock.

### Online presence

With a bot token saved, ViewDock also opens an outbound connection to the Discord Gateway so the bot shows as online, **Watching movies & TV**. It uses the active bot token: the sign-in application's, or the separate one when that option is on. Saving a different token or switching the option reconnects with the new credentials. The connection only sets presence; it requests no privileged intents, and slash commands, verification and party invites keep arriving through the HTTPS interactions endpoint, so they work even when the Gateway is offline.

The connection sends heartbeats, resumes after network drops and retries with increasing delays when Discord is unavailable. If Discord rejects the token, ViewDock waits for you to save a new one, checking again only every 30 minutes. It needs no extra container, port or tunnel configuration, and a missing or invalid token never stops ViewDock from starting. The **Gateway (online presence)** group in the diagnostics panel shows the connection, the presence, when it last connected and any error, with a **Reconnect now** button.

The bot token of the sign-in application can also be provided with `VD_DISCORD_BOT_TOKEN`; a value saved in the admin page takes precedence. The separate bot configuration is set on the admin page only. Tokens and secrets are stored encrypted with the [master key](environment.md).

### Commands

| Command | What it does |
|---------|--------------|
| `/party create title:<title>` | Starts a watch party for a title and links it to the current channel |
| `/party invite` | Posts the invite link for the party linked to this channel |
| `/party status` | Shows the state of the linked party |
| `/party pause`, `/party resume` | Pauses or resumes the party (host only) |
| `/party link-voice channel:<voice channel>` | Links a voice channel to a party (host only) |
| `/party unlink` | Removes a channel's party link |

People must link their Discord account to ViewDock (through Discord sign-in or **Settings → Connected**) before they can use the commands. Anyone who has not linked an account gets a private reply explaining how.

Administrators can also post a party invite into any channel with **Send party invite** on the Discord page, and see which channels are linked to which parties.

## Discord Activity

The Activity opens ViewDock inside a Discord voice channel. Everyone who launches it from the same channel lands in that channel's watch party, and each person streams from your ViewDock server with their own account, as in a browser. Discord bots cannot send video into a call, so this is how ViewDock plays inside Discord.

The Activity runs on the sign-in application and needs:

- Discord sign-in credentials (client ID and secret) and the bot token, with the separate bot configuration turned off. ViewDock uses the bot to confirm who is in an Activity before it reveals a party.
- An HTTPS public URL, and watch parties turned on.

To set it up:

1. In the Developer Portal, open the sign-in application, then **Activities → Settings**, and enable Activities.
2. Under **Activities → URL Mappings**, map the root prefix `/` to your public host, for example `viewdock.example.com`.
3. If the public host sits behind a login proxy such as Cloudflare Access, allow requests from Discord's proxy through; the Activity cannot complete an interactive proxy login. ViewDock still requires everyone to sign in.
4. In **Admin → Discord**, turn on **Discord Activity**. The **Discord Activity** group in the diagnostics panel checks the requirements and whether Activities are enabled for the application.

Discord adds the Activity's launch button (its entry point command) when you enable Activities. Registering slash commands globally from ViewDock keeps it, and adds it back if it is missing. If the Developer Portal warns that the app "has no commands registered to launch" the Activity, press **Register commands** on the Discord page with the server ID left blank.

When someone opens the Activity, ViewDock signs them in with the Discord account running it, following the same rules as Discord sign-in on the web: registration restrictions apply to new accounts, and with Discord sign-in turned off only accounts already linked to Discord can use it. The first person in the voice channel to open the Activity hosts it and picks the title; everyone else in the channel waits and joins that party automatically when it starts. An administrator who opens the Activity later takes over as host, before the party starts or while it plays; one administrator does not take over from another. When the host leaves the call, the next person to open the Activity hosts. Accounts that can only join parties never host.

**Leave party** in the party panel (or closing the player) takes you out of the channel's party: you can then watch something on your own from the Activity's start page, and **Rejoin** there puts you back in the party while it is still playing. If that party ends and a new one starts, you are taken into the new one.

Inside Discord, ViewDock's cookies are issued as partitioned third-party cookies, so the Activity session is separate from your normal browser session. The service worker is not installed inside Discord, so the app always loads the current build. The Activity must point at a control process; the separate `VD_ROLE=frontend` process does not allow framing.
