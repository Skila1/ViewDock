# ViewDock

**Mount a folder or upload a video. ViewDock handles the rest.**

ViewDock is a lightweight, private, self-hosted video platform for movies and TV. One Docker container. SQLite. Your files stay on your disk.

```yaml
services:
  viewdock:
    image: ghcr.io/viewdock/viewdock:latest
    container_name: viewdock
    ports:
      - "8080:8080"
    volumes:
      - ./config:/config
      - ./cache:/cache
      - ./transcode:/transcode
      - /mnt/media:/media:ro
    restart: unless-stopped
```

License: **GNU Affero General Public License v3.0 or later** (`AGPL-3.0-or-later`).

## Documentation

- [Installation](docs/install.md)
- [Docker](docs/docker.md)
- [Environment](docs/environment.md)
- [Reverse proxy](docs/reverse-proxy.md)

## Development

FFmpeg/ffprobe on `PATH` enables remux, transcode, and accurate probes. Filename catalogue and Direct Play of the original file work without them.

The public marketing site lives in the sibling `../Marketing` folder (Astro, port 8085) and is served at [viewdock.dev](https://viewdock.dev). The app is typically `https://app.viewdock.dev` behind your own reverse proxy or Cloudflare Tunnel — cloudflared is not part of this Compose file.

```bash
cd web && npm install && npm run build && cd ..
go run ./cmd/viewdock
```

Open http://127.0.0.1:8080
