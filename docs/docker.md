# Docker

The image is Debian + ffmpeg + libzimg (`zscale`). VAAPI packages install on `linux/amd64` only.

- Image: `ghcr.io/skila1/viewdock:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- Tags: `latest` on the default branch, the `VERSION` file, git SHA, and semver from `v*` tags
- NVIDIA / jellyfin-ffmpeg overlay is a later image (`:nvidia`), not required to start

Production hosts should use the [one-line installer](install.md), then:

```bash
cd ~/viewdock
docker compose pull && docker compose up -d
```

The repo `docker-compose.yml` is for development (`build:`). The installer writes a pull-only Compose file with `./update` and the Docker socket so **Admin → Updates** can recreate the container.

```bash
curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh | sudo bash
```

## Volumes

| Host | Container | Notes |
|------|-----------|--------|
| `./config` | `/config` | SQLite only |
| `./cache` | `/cache` | artwork + HLS |
| `./transcode` | `/transcode` | in-flight jobs |
| media folder | `/media:ro` | never rewritten; never chowned |

## User

`PUID` / `PGID` map the process user. Entrypoint chowns **directory inodes** of `/config`, `/cache`, `/transcode` only. `/media` is never walked.

If `/dev/dri` exists and the process starts as root, those device GIDs are added as supplementary groups.

## Health

`wget http://127.0.0.1:8080/healthz`. Compose `stop_grace_period` should be 60s so FFmpeg children can exit.
