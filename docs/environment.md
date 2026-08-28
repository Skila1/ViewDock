# Environment

`.env` is only for the host port and the public URL. Paths, trusted proxies, cookies, TMDB, Discord, and updates are defaults or Admin settings.

| Variable | Default | Meaning |
|----------|---------|---------|
| `VD_PORT` | `8080` | Host port published by Docker |
| `VD_PUBLIC_URL` | empty | Public origin (Discord OAuth, share links). Example: `https://app.viewdock.dev`. Also editable under **Admin → Settings** |

Trusted proxies are always loopback, private LAN/Docker ranges, and Cloudflare edge IPs. Cookie `Secure` follows TLS / `X-Forwarded-Proto` from those proxies.

Installer-only (not written to `.env`): `VD_UNATTENDED=1` skips the TUI. `VD_IMAGE` / `VD_INSTALL_URL` select what the installer itself pulls.
