# Docker

The image is Debian + ffmpeg + libzimg (`zscale`). VAAPI packages install on `linux/amd64` only.

- Image: `ghcr.io/skila1/viewdock:latest` (same tag on CPU and GPU hosts)
- Platforms: `linux/amd64`, `linux/arm64`
- Tags: `latest` on the default branch, the `VERSION` file, git SHA, and semver from `v*` tags
- One Compose file. Set `VD_GPU=true` or `VD_GPU=false` in `.env`. The installer sets `COMPOSE_PROFILES` to match (`gpu` or `cpu`) so CPU hosts never request an NVIDIA device.
- `VD_GPU=true` needs the NVIDIA Container Toolkit on the host. ViewDock does not install drivers or the toolkit.

Production hosts should use the [one-line installer](install.md), then:

```bash
cd ~/viewdock
docker compose pull && docker compose up -d
```

The repo `docker-compose.yml` is the production pull file. The installer writes the same shape (plus `./update` and the Docker socket) so **Admin → Updates** can recreate the container. Re-running the installer or `viewdock update` keeps an existing `.env` and an already-migrated compose file; it only adds missing keys and upgrades the old overlay layout. `VD_GPU` is never overwritten. On a first install, a working NVIDIA Docker runtime sets `VD_GPU=true`.

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
