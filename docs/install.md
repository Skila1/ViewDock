# Install ViewDock

ViewDock is one container. Media stays on your disk.

## Requirements

- Docker Engine 24+
- A folder of movies and/or TV files
- Optional: [TMDB API key](https://www.themoviedb.org/settings/api) for posters and plots

## Quick start

```yaml
services:
  viewdock:
    image: ghcr.io/viewdock/viewdock:latest
    ports:
      - "8080:8080"
    volumes:
      - ./config:/config
      - ./cache:/cache
      - ./transcode:/transcode
      - /mnt/media:/media:ro
    environment:
      PUID: "1000"
      PGID: "1000"
    stop_grace_period: 60s
    restart: unless-stopped
```

```bash
docker compose up -d
```

Open http://127.0.0.1:8080 and complete the first-run wizard. Library type (`movies`, `tv`, or `mixed`) is required. TMDB can be skipped.

The public site is [viewdock.dev](https://viewdock.dev). A typical app hostname is `app.viewdock.dev` behind your own reverse proxy or Cloudflare Tunnel (not included in this Compose file).

## Backup

Stop the stack, then copy `/config`:

```bash
docker compose stop
cp -a ./config ./config-backup
docker compose start
```

Do not copy the database while the container is writing. Live checkpoint-copy is not supported.

## From source

```bash
cd web && npm install && npm run build && cd ..
go run ./cmd/viewdock
```
