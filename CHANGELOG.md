# Changelog

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
- Admin → API keys issues `vd_…` bearer tokens (SoundDock-style). Admin → Logs stores application and playback events and is readable over the REST API.
- The player has an exit control. HLS waits for the first playlist instead of looping 410s.
