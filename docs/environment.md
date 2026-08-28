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
| `VD_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | Who may set `X-Forwarded-*` |
| `VD_COOKIE_SECURE` | unset | Force `Secure` on cookies |
| `VD_LAN_CIDRS` | RFC1918 + loopback | Treated as LAN for bitrate/quality |
| `VD_TMDB_API_KEY` | empty | Overrides UI key |
| `VD_SQLITE_BUSY_TIMEOUT_MS` | `20000` | SQLite busy_timeout |
| `VD_SHUTDOWN_WAIT` | `45s` | HTTP shutdown timeout |
| `PUID` / `PGID` | `1000` | Container user |
| `TZ` | `UTC` | Timezone |
