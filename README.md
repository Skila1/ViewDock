# ViewDock

**Mount a folder or upload a video. ViewDock handles the rest.**

ViewDock is a private, self-hosted video platform for movies and TV. Stream the files you already own to any browser, watch together in sync, and download titles for offline viewing. Your media stays on your disk and is never modified.

One Docker container. SQLite. Optional NVIDIA transcoding.

## What you can do

- Add movies and TV from folders on the host, or upload videos in **Admin → Uploads** (10 GB per file).
- Play in the browser. Direct Play of the original file works without FFmpeg. FFmpeg on `PATH` adds probing, remux, and transcode.
- Watch together, with progress, resume, and My List kept per account.
- Download titles for offline viewing, within the policy set for that account.
- Sign in with Discord, start a party with the Discord bot, or watch a voice channel's party in the Discord Activity.
- Attach a Jellyfin server and play those libraries from ViewDock.
- Offer a Windows app that each server builds for itself.
- Manage users, households, and content ratings, and schedule backups of the database and settings.

Full documentation is at [wiki.viewdock.dev](https://wiki.viewdock.dev/). The source is in [`docs/`](docs/).

## Install

On a Linux host:

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

The installer writes a Docker Compose project in `./viewdock` under the current directory (`~/viewdock` if you run it from home). If you are already in a folder named `viewdock`, it installs there. It installs Docker if it is missing and starts ViewDock on port 8080. Cloudflare Tunnel is optional. Discord and TMDB are configured in the web Admin after the first launch.

Unattended (no prompts):

```bash
sudo env VD_UNATTENDED=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

Then:

```bash
cd ~/viewdock
docker compose ps
docker compose logs -f
docker compose pull && docker compose up -d
```

Open `http://<host>:8080`. The first start prints an 8-character setup token in the console and in `docker compose logs` (`ViewDock setup token:`). Use it to create the administrator account.

Optional helper, from that same directory: `sudo viewdock status|update|logs|doctor|uninstall`.

Production hosts should pull the published image (`ghcr.io/skila1/viewdock:latest`) with the installer or `docker compose pull`, rather than cloning this repository. To reach ViewDock from the internet, put it behind your own reverse proxy or Cloudflare Tunnel. The project website is [viewdock.dev](https://viewdock.dev).

## Documentation

- [Installation](docs/install.md)
- [Docker](docs/docker.md)
- [Environment](docs/environment.md)
- [Reverse proxy](docs/reverse-proxy.md)
- [Discord](docs/discord.md)
- [Windows app](docs/desktop.md)
- [Uploads](docs/uploads.md)
- [Backups and restore](docs/backups.md)
- [Upgrade](docs/upgrade.md)
- [REST API](docs/api.md)
- [Development](docs/development.md)

## Build from source

Requirements and the repository layout are in [Development](docs/development.md).

```bash
cd web && npm install && npm run build && cd ..
go run ./cmd/viewdock
```

Open http://127.0.0.1:8080

To build the container from this checkout:

```bash
cp .env.example .env
docker compose up -d --build
```

## License

ViewDock is licensed under the [PolyForm Noncommercial License 1.0.0](https://polyformproject.org/licenses/noncommercial/1.0.0). The full terms are in [`LICENSE`](LICENSE).

Required Notice: Copyright 2026 ViewDock

You may use, change, and share this software for noncommercial purposes. That covers personal use (including private entertainment, study, research, and hobby projects) and use by charitable organizations, educational institutions, public research organizations, public safety or health organizations, environmental protection organizations, and government institutions, as those terms are defined in the license. Commercial use is not permitted.

If you distribute any part of ViewDock, include these license terms, or the URL above, and the Required Notice.
