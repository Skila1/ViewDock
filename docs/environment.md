# Environment

`.env` is for the host port, the public URL, and whether the container may use an NVIDIA GPU. Paths, trusted proxies, cookies, TMDB, Discord, and updates are defaults or Admin settings.

| Variable | Default | Meaning |
|----------|---------|---------|
| `VD_PORT` | `8080` | Host port published by Docker |
| `VD_PUBLIC_URL` | empty | Public origin (Discord OAuth, share links). Example: `https://app.viewdock.dev`. Also editable under **Admin → Settings** |
| `VD_GPU` | `false` | `true` or `false`. When `true`, Compose starts the GPU profile (`gpus: all` + NVENC env). Needs the NVIDIA Container Toolkit |
| `COMPOSE_PROFILES` | `cpu` | Kept in sync with `VD_GPU` (`cpu` or `gpu`). Set this too if you edit `VD_GPU` by hand, then `docker compose up -d --remove-orphans` |

Trusted proxies are always loopback, private LAN/Docker ranges, and Cloudflare edge IPs. Cookie `Secure` follows TLS / `X-Forwarded-Proto` from those proxies.

Installer-only (not written to `.env`): `VD_UNATTENDED=1` skips the TUI. `VD_IMAGE` / `VD_INSTALL_URL` select what the installer itself pulls.
