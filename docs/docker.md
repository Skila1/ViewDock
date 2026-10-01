# Docker

The image is Debian + ffmpeg + libzimg (`zscale`). VAAPI packages install on `linux/amd64` only.

- Image: `ghcr.io/skila1/viewdock:latest` (same tag on CPU and GPU hosts)
- Platforms: `linux/amd64`, `linux/arm64`
- Tags: `latest` on the default branch, the `VERSION` file, git SHA, and semver from `v*` tags
- Images are built from pushes to `main` whose commit message starts with `DEPLOY:`. Each one is a release: the patch version in `VERSION` goes up (unless the push already changed it), the `## Unreleased` section of `CHANGELOG.md` becomes that version, and a `Release X.Y.Z` commit is pushed to `main` before the image is built, so **Admin → Updates** offers it
- One Compose file. Set `VD_GPU=true` or `VD_GPU=false` in `.env`. The installer sets `COMPOSE_PROFILES` to match (`gpu` or `cpu`) so CPU hosts never request an NVIDIA device.
- `VD_GPU=true` needs the NVIDIA Container Toolkit on the host. ViewDock does not install drivers or the toolkit.

Production hosts should use the [one-line installer](install.md), then:

```bash
cd ~/viewdock
docker compose pull && docker compose up -d
```

The repo `docker-compose.yml` is the production pull file. The installer writes the same shape (plus `./update` and the Docker socket) so **Admin → Updates** can recreate the container. Re-running the installer or `viewdock update` keeps an existing `.env` and an already-migrated compose file; it only adds missing keys and upgrades the old overlay layout. `VD_GPU` is never overwritten. On a first install, a working NVIDIA Docker runtime sets `VD_GPU=true`.

For a local distributed profile, use `COMPOSE_PROFILES=distributed docker compose up -d`. This starts ViewDock with PostgreSQL and MinIO; override `VD_DATABASE_URL` or the `VD_STORAGE_*` variables for external services.

The distributed profile uses the public `docker.io/bitnamilegacy/minio:latest` image. It runs as root only to accommodate Docker Desktop named-volume permissions; production operators should use a managed S3-compatible service or a volume with an explicitly configured non-root owner.

```bash
curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh | sudo bash
```

## Volumes

| Host | Container | Notes |
|------|-----------|--------|
| `./config` | `/config` | SQLite only |
| `./cache` | `/cache` | artwork + HLS |
| `./transcode` | `/transcode` | in-flight jobs |
| media folder | `/media` | writable: ViewDock creates library folders here, and uploads and moves write here |

## User and folder permissions

The container starts as root, prepares its folders, then runs ViewDock as `PUID:PGID` (default `1000:1000`). Do not set `user:` in Compose; the entrypoint needs root for this one step.

On every start (first install, `docker compose up -d` after an update, container recreation, host reboot) the entrypoint:

- gives `/config`, `/config/uploads`, `/cache`, `/transcode` and `/update` to `PUID:PGID` (the folders themselves, not their contents);
- runs `viewdock prepare-storage`, which gives `PUID:PGID` the media root (`/media` and any `VD_LIBRARY_ROOTS`) and every folder under it, up to four levels deep, that **root** created — for example a `movies` folder made with `mkdir` over SSH. Files are never changed, and folders owned by any other account are left alone. `VD_FIX_PERMISSIONS=false` turns this off; `VD_FIX_PERMISSIONS_DEPTH` changes the depth;
- adds the group ids of `/dev/dri` devices and the Docker socket to the ViewDock account, so hardware transcoding and in-app updates keep working as a non-root user.

Library folders themselves are created by ViewDock when you add a library (**Admin → Media → Libraries**), with mode `0755` and owner `PUID:PGID`. You never need to create, `chown` or `chmod` a library folder by hand.

## Health

`wget http://127.0.0.1:8080/healthz`. Compose `stop_grace_period` should be 60s so FFmpeg children can exit.
