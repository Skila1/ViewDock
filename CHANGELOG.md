# Changelog

## 0.1.10

- Admin, Media brings together Libraries (add, edit, scan and delete local libraries), Titles (search every movie and series, match it on TMDB, upload a poster, set its rating), Uploads, Library access and Jellyfin servers.
- Media, Settings and Discord get their own sidebar in the admin area, with Back to admin at the top and their sections grouped below.
- Adding a Jellyfin server now uses a Save button. Each saved server has a What to sync box listing its Jellyfin libraries by type (Movies, TV shows, Anime, Mixed), to sync everything or only chosen libraries.
- Continue Watching shows titles and posters, and no longer shows items that were removed or that the viewer cannot access.

## 0.1.9

- Admins can connect Jellyfin servers under Admin, Media sources. Their movies and shows (with posters and metadata) appear for every user and resync every 6 hours. A title that is also in a local library is listed once, plays locally by default, and the player can switch to the Jellyfin copy. Jellyfin playback supports seeking and works in watch parties. Credentials stay on the server.
- Jellyfin sources can use an API key instead of an account. Each source has usage restrictions (posters, streaming, transcoding, activity log, maximum streams) that ViewDock enforces on itself, and a usage log of what ViewDock did with the source, plus optional Jellyfin activity for the ViewDock user.
- The Watch Together panel can be closed, and the host chooses whether it is shown to everyone, only the host, or nobody. A party button in the player controls reopens it.
- Admin, Watch parties lists web and Discord Activity parties. Admins can remove a member, block them from rejoining, end a party, and read each member's playback session and client logs.
- When a browser or Discord refuses to play a stream, the player now explains how to turn on hardware acceleration instead of showing a browser error.

## 0.1.8

- The close button in a watch party works inside the Discord Activity, and in a party opened from a link. In the Activity it returns to the title picker, with a Rejoin button while others are still watching.
- The Discord Activity no longer reopens the last movie when nobody is watching it; the channel picks a new title instead.

## 0.1.7

- The Discord Activity shows your movies and TV as the same poster grid as the home page, so you can pick a title without searching. Search narrows the grid.

## 0.1.6

- Registering slash commands globally adds the Discord Activity's launch command back when Activities are enabled but Discord has none, for example after an earlier registration removed it.

## 0.1.5

- Admin → Updates shows "Updated" as soon as the host finishes, instead of about two minutes later, and the log only shows the current run.
- Opening Admin replaces the main sidebar with the admin sidebar: **Back to app** first, then Overview and the admin pages in groups. Small screens get the same links as a scrollable bar.
- Every admin page uses the card grid layout from the Discord page. Overview is a dashboard with streams, users, libraries, node health and the version.
- Duplicate admin settings removed. Discord settings are only on the Discord page, not also under Settings. Resilience no longer repeats the live sessions shown under Streams. Library access (including the new per-person and per-group **Downloads** toggle) is edited only under Grants, and group membership only under Groups; Users shows both read-only with links.
- New **Discord Activity**: open ViewDock from a voice channel and the channel watches its party together, each person streaming with their own account. It reuses the sign-in application and bot, signs people in with the Discord account running the Activity under the usual sign-in rules, and is off by default. Setup steps are on the Discord page and in the Discord guide.
- Removed the Labs virtual camera, RTMP and SRT broadcaster and its admin page. Discord bots cannot send video, so watching inside Discord now uses the Activity. An upgrade deletes the saved Labs settings, including any stored output URL.

## 0.1.4

- The Discord bot now shows as online, watching movies & TV. ViewDock keeps a lightweight Gateway connection with the active bot token (shared or separate) that reconnects and resumes on its own. Slash commands, verification and party invites still use the HTTP interactions endpoint; the Gateway uses no privileged intents and handles no interactions.
- Admin → Discord diagnostics show the Gateway connection, presence and last connection separately from the HTTP bot and interactions checks, with a "Reconnect now" action and a clear message when Discord rejects the token.

## 0.1.3

- Admin → Updates no longer shows "Updating" when nothing is running. The host helper's systemd unit could create `update/request` as a directory, which looked like an update that never finished; it is removed, and an update the host does not pick up now fails after 2 minutes with a hint instead of waiting 30.
- The host helper no longer writes "no request" to `update/last.log` every few seconds; only real update runs are logged.
- Check now no longer overwrites the status of an update started while the check was running.
- Re-running the installer or `sudo viewdock update` pulls the image before stopping ViewDock, so a failed pull leaves the app running.
- The web app manifest is requested with credentials, so it loads behind Cloudflare Access.

## 0.1.2

- Admin → Discord is reorganised into cards with a status and diagnostics panel that checks credentials, sign-in, the bot's servers and permissions, slash commands and the interactions endpoint, with a fix for each problem.
- Optional "Use separate Discord bot configuration" for running the bot from a different Discord application. Off by default; existing settings keep working unchanged.
- Every `DEPLOY:` release bumps the patch version automatically, so Admin → Updates offers each new image.

## 0.1.1

- Updates page only reports an update when GitHub `VERSION` is newer than the installed version. Same version is up to date, even if the `:latest` digest changed.
- `sudo viewdock update` refreshes `install.sh`, pulls the image, and recreates the container. SQLite in `./config` and `./media` stay on disk.
- Superadmin role, user delete in Admin → Users, and Discord guild/role whitelist. Enabling Discord turns off all local login and signup. Set the Superadmin Discord user ID before enabling.

## 0.1.0

- First public image on `ghcr.io/skila1/viewdock`. Tags: `latest` (default branch), the `VERSION` file, git SHA, and semver from `v*` tags.
- One-line installer writes a Compose project in `./viewdock`, installs Docker if missing, and starts the stack. Optional Cloudflare Tunnel is a systemd service, not a container.
- Admin → Updates checks GHCR digest plus GitHub `VERSION` / `CHANGELOG.md`. Update now uses the host helper (`viewdock-update`) or the Docker socket.
- Automatic updates can pull a newer image about once an hour when the helper or socket is available.
- SQLite, one container. Media stays on your disk. First-run prints an 8-character setup token in the console and logs after the server is listening.
- `.env` only has `VD_PORT` and `VD_PUBLIC_URL`. Trusted proxies include local/Docker and Cloudflare. Public URL and TMDB are also under Admin → Settings.
- Administrators can upload videos (max 10 GB) under Admin → Uploads. Staging is `/config/uploads`. `/media` is read-write so finished files can land in the library.
- Admin → API keys issues `vd_…` bearer tokens. Admin → Logs stores application and playback events and is readable over the REST API.
- The player has an exit control. HLS waits for the first playlist instead of looping 410s.
