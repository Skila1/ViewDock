# Environment

`.env` is for the host port, the public URL, and whether the container may use an NVIDIA GPU. Paths, trusted proxies, cookies, TMDB, Discord, and updates are defaults or Admin settings.

| Variable | Default | Meaning |
|----------|---------|---------|
| `VD_PORT` | `8080` | Host port published by Docker |
| `VD_PUBLIC_URL` | empty | Public origin (Discord OAuth, share links). Example: `https://viewdock.example.com`. Also editable under **Admin → Settings** |
| `VD_GPU` | `false` | `true` or `false`. When `true`, Compose starts the GPU profile (`gpus: all` + NVENC env). Needs the NVIDIA Container Toolkit |
| `COMPOSE_PROFILES` | `cpu` | Kept in sync with `VD_GPU` (`cpu` or `gpu`). Set this too if you edit `VD_GPU` by hand, then `docker compose up -d --remove-orphans` |
| `VD_MASTER_KEY` | empty | Optional base64-encoded 32-byte key (for example `openssl rand -base64 32`) that encrypts stored secrets such as the TMDB key, Discord client secret and bot token. When empty, ViewDock creates `/config/master.key` (mode 0600) on first start. Back this key up with the database: without it, stored secrets cannot be decrypted and must be re-entered |
| `VD_DISCORD_BOT_TOKEN` | empty | Default Discord bot token. A value saved under **Admin → Settings** takes precedence |
| `VD_BACKUP_SCHEDULE_HOURS`, `VD_BACKUP_RETENTION`, `VD_BACKUP_DESTINATION`, `VD_BACKUP_S3_*` | `24`, `7`, `local` | Initial backup schedule, retention and destination; editable under **Admin → Settings → Backups**. See [backups.md](backups.md) |
| `VD_ROLE` | `all` | `all` serves everything from one process. `control` serves the app and API and places all playback on registered workers. `worker` runs only the signed playback API for a control plane; it needs `VD_NODE_SECRET` and the same database. `frontend` serves the web app and relays `/api`, `/hls` and `/mesh` to `VD_CONTROL_URL`; it opens no database. `coordinator` hosts watch party rooms for control planes that set `VD_COORDINATOR_URL`; it needs `VD_COORDINATOR_SECRET` and the same database |
| `VD_COORDINATOR_URL` | empty | On `all` or `control`: base URL of a separate `VD_ROLE=coordinator` process (for example `http://viewdock-coordinator:8080`). When empty, watch parties run inside the control plane |
| `VD_COORDINATOR_SECRET` | empty | Shared secret (at least 32 characters) that signs requests between control planes and the coordinator. Required on both sides when a separate coordinator is used |
| `VD_CONTROL_URL` | empty | Required with `VD_ROLE=frontend`: base URL of the control plane, `http` or `https` with a host and optional port only (for example `http://viewdock-control:8080`) |
| `PUID`, `PGID` | `1000` | Account ViewDock runs as inside the container, and owner of the folders it creates |
| `VD_LIBRARY_ROOTS` | empty | Extra storage roots (mounted into the container) that library folders may live in, separated by `:` or `,`. `VD_MEDIA_DIR` (`/media`) is always allowed. Libraries can never point outside these roots |
| `VD_FIX_PERMISSIONS` | `true` | On start, give the ViewDock account the media roots and any library, show or season folder that root created in them. Files and folders owned by other accounts are never changed. `false` turns it off |
| `VD_FIX_PERMISSIONS_DEPTH` | `4` | How many folder levels below each media root that start-up check visits |
| `VD_NODE_SECRET` | empty | Worker credential issued under **Admin → Nodes** (at least 32 characters). Workers reject every request not signed with it, except `/healthz` |

### Media workers

With `VD_ROLE=all`, playback stays local unless **Admin → Settings → Play on registered media workers** is on. With `VD_ROLE=control`, every new session goes to a worker. The control plane picks the healthy, non-draining node in the highest priority tier, spreading sessions by weight inside a tier, and tries the next candidate if a worker refuses or fails. Browsers never talk to workers directly: media flows through `/mesh/{node}/...` on the control origin, authorized by the per-session stream token. If a worker dies, its sessions answer `410 NODE_UNAVAILABLE` and the player recreates the session on another worker at the current position. Nodes are probed every 5 seconds. A connection failure marks a node down at once; other errors need two consecutive failures. Inside a priority tier, measured reliability also counts: a worker scoring more than 10 points below the best in its tier is tried only after the others, and reliability overrides (prefer, avoid, exclude) set under **Admin → Resilience** apply to placement.

### Separate frontend

A `VD_ROLE=frontend` container keeps the app shell, service worker and assets available while the control plane restarts or is down; API calls then answer `502 control_unavailable` with `Retry-After`, which the offline mode in the app already handles. It forwards the client address to the control plane, and the control plane honours it only from trusted proxy ranges (loopback and private networks by default), so run the frontend on the same private network. `/readyz` on the frontend reports whether the control plane is reachable. The Compose `mesh` profile includes one on port `VD_FRONTEND_PORT` (default 8081).

### Separate watch party coordinator

With `VD_COORDINATOR_URL` set, the control plane hosts no rooms. It relays the watch party routes and room WebSocket to the coordinator, which signs users in against the shared database, and asks the coordinator (with signed requests) for party access checks, Discord party commands and the Resilience dashboard. Rooms, members and the timeline are stored in the database, so either process can restart without losing a party. While the coordinator is down, party requests answer `503 coordinator_unavailable`, the player shows "Not synchronized, reconnecting" and rejoins when it returns, party-only accounts cannot start new streams, and everything else keeps working. Enable it with `COMPOSE_PROFILES=mesh,coordinator` and the two variables above in `.env`.

Node addresses may be hostnames, loopback or private addresses; link-local, multicast and cloud metadata addresses are rejected, including when a hostname resolves to one. Use `https` for nodes outside a trusted LAN. The Compose `mesh` profile runs a control plane with two workers; see the comments in `docker-compose.yml`.

Routine settings (public URL, TMDB key, transcode slots, feature flags, sync thresholds, guest limits, log retention) are edited at runtime under **Admin → Settings**. A saved value overrides the environment; removing it falls back to the environment, then the built-in default. Every change is versioned and can be restored from the history list.

Trusted proxies are always loopback, private LAN/Docker ranges, and Cloudflare edge IPs. Cookie `Secure` follows TLS / `X-Forwarded-Proto` from those proxies.

Installer-only (not written to `.env`): `VD_UNATTENDED=1` skips the TUI. `VD_IMAGE` / `VD_INSTALL_URL` select what the installer itself pulls.
