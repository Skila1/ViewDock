# Docker

The image is Debian + ffmpeg + libzimg (`zscale`). VAAPI packages install on `linux/amd64` only.

- Image: `ghcr.io/skila1/viewdock:latest` (same tag on CPU and GPU hosts)
- Platforms: `linux/amd64`, `linux/arm64`
- Tags: `latest` on the default branch, the `VERSION` file, git SHA, and semver from `v*` tags
- CPU-only: `docker-compose.yml` only (no `gpus:`, no NVIDIA env)
- GPU hosts: add `docker-compose.gpu.yml` via `COMPOSE_FILE=docker-compose.yml:docker-compose.gpu.yml` or `docker compose -f docker-compose.yml -f docker-compose.gpu.yml`. ViewDock does not install the NVIDIA Container Toolkit or drivers.

Production hosts should use the [one-line installer](install.md), then:

```bash
cd ~/viewdock
docker compose pull && docker compose up -d
```

The repo `docker-compose.yml` is the CPU-safe production pull file. The installer writes the same shape (plus `./update` and the Docker socket) so **Admin → Updates** can recreate the container. On a host with a working NVIDIA Docker runtime, the installer also writes `docker-compose.gpu.yml` and sets `COMPOSE_FILE`. Re-running the installer on a CPU-only host drops that overlay from `.env`.

```bash
curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh | sudo bash
```

## Volumes

| Host | Container | Notes |
|------|-----------|--------|
| `./config` | `/config` | SQLite only |
| `./cache` | `/cache` | artwork + HLS |
| `./transcode` | `/transcode` | in-flight jobs |
| media folder | `/media` | writable so Admin uploads can land here. Entrypoint chowns the directory inode only; it never walks files |

## User

`PUID` / `PGID` map the process user. Entrypoint chowns **directory inodes** of `/config`, `/cache`, `/transcode`, `/config/uploads`, and `/media` only. Media files are never walked.

If `/dev/dri` exists and the process starts as root, those device GIDs are added as supplementary groups.

## Health

`wget http://127.0.0.1:8080/healthz`. Compose `stop_grace_period` should be 60s so FFmpeg children can exit.
