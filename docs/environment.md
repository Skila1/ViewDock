# Environment

Prefix: `VD_*`.

| Variable | Default | Meaning |
|----------|---------|---------|
| `VD_HTTP_ADDR` | `:8080` | Listen address |
| `VD_CONFIG_DIR` | `./config` | SQLite directory |
| `VD_DATABASE_PATH` | `$VD_CONFIG_DIR/viewdock.db` | Database file |
| `VD_CACHE_DIR` | `./cache` | Artwork + HLS |
| `VD_TRANSCODE_DIR` | `./transcode` | Temp transcodes |
| `VD_MEDIA_DIR` | `./media` | Default media root (extra binds still under `/media`) |
| `VD_LOG_LEVEL` | `info` | slog level |
| `VD_PUBLIC_URL` | empty | Public app URL (Discord OAuth redirect). Production: `https://app.viewdock.dev`. Does **not** force Secure cookies |
| `VD_DISCORD_CLIENT_ID` | empty | Optional bootstrap of Discord OAuth client ID into Admin settings |
| `VD_DISCORD_CLIENT_SECRET` | empty | Optional bootstrap of Discord client secret |
| `VD_DISCORD_LOGIN` | `0` | Set `1` to enable Discord sign-in when bootstrapping from env |
| `VD_SETUP_TOKEN` | empty | First-admin bootstrap token. If unset, a token is written to `$VD_CONFIG_DIR/setup.token` and is **not** logged or returned by the API |
| `VD_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Who may set `X-Forwarded-*` |
| `VD_COOKIE_SECURE` | unset | Force `Secure` on cookies |
| `VD_LAN_CIDRS` | RFC1918 + loopback | Treated as LAN for bitrate/quality |
| `VD_TMDB_API_KEY` | empty | Overrides UI key |
| `VD_SQLITE_BUSY_TIMEOUT_MS` | `20000` | SQLite busy_timeout |
| `VD_SHUTDOWN_WAIT` | `45s` | HTTP shutdown timeout |
| `PUID` / `PGID` | `1000` | Container user |
| `TZ` | `UTC` | Timezone |
| `VD_IMAGE` | `ghcr.io/skila1/viewdock:latest` | Image the updater pulls and checks |
| `VD_UPDATE_DIR` | `/update` | Bind-mount for the host update helper |
| `VD_UPDATE_PREFIX` | empty | Host install directory (set by systemd helper) |
| `VD_COMPOSE_PROJECT` | `viewdock` | Compose project label used by the socket fallback |
| `VD_DOCKER_GID` | docker.sock gid | `group_add` so the container can talk to Docker |
| `VD_VERSION_URL` | `https://raw.githubusercontent.com/Skila1/ViewDock/main/VERSION` | Override remote version check |
| `VD_CHANGELOG_URL` | `https://raw.githubusercontent.com/Skila1/ViewDock/main/CHANGELOG.md` | Override changelog fetch |
| `VD_INSTALL_URL` | `https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh` | Override installer re-fetch |
| `VD_UNATTENDED` | unset | `1` skips the installer TUI |
