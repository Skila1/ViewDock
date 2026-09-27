# Changelog

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
