# ViewDock

**Mount a folder or upload a video. ViewDock handles the rest.**

ViewDock is a lightweight, private, self-hosted video platform for movies and TV. One Docker container. SQLite. Your files stay on your disk.

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

A whiptail installer (same idea as Proxmox helper scripts) writes a Docker Compose project in `./viewdock` under your current directory (`~/viewdock` if you run it from home). If you are already in a folder named `viewdock`, it installs there. It installs Docker if missing and starts the stack. Docker publishes port 8080. Cloudflare Tunnel is optional. Discord is configured in the web Admin after first launch. Then you manage it like any Compose stack:

```bash
cd ~/viewdock
docker compose ps
docker compose logs -f
docker compose down
docker compose pull && docker compose up -d
```

Optional helper: `sudo viewdock status|update|logs|doctor|uninstall` (same directory).

Unattended (no TUI):

```bash
sudo env VD_UNATTENDED=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

License: **GNU Affero General Public License v3.0 or later** (`AGPL-3.0-or-later`).

## Documentation

Full documentation is at [wiki.viewdock.dev](https://wiki.viewdock.dev/). The source lives in [`docs/`](docs/):

- [Installation](docs/install.md)
- [Docker](docs/docker.md)
- [Environment](docs/environment.md)
- [Reverse proxy](docs/reverse-proxy.md)
- [Discord](docs/discord.md)
- [Backups and restore](docs/backups.md)
- [REST API](docs/api.md)
- [Development](docs/development.md)

## Development

Production hosts should `docker compose pull && docker compose up -d` so they use `ghcr.io/skila1/viewdock:latest` instead of cloning.

```bash
cp .env.example .env
docker compose up -d --build
```

FFmpeg/ffprobe on `PATH` enables remux, transcode, and accurate probes. Filename catalogue and Direct Play of the original file work without them.

To reach ViewDock from the internet, put it behind your own reverse proxy or Cloudflare Tunnel; cloudflared is not part of this Compose file. The project website is [viewdock.dev](https://viewdock.dev).

```bash
cd web && npm install && npm run build && cd ..
go run ./cmd/viewdock
```

Open http://127.0.0.1:8080
