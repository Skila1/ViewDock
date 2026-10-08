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
| `./cache` | `/cache` | artwork, HLS, and copies of Jellyfin titles (see below) |
| `./transcode` | `/transcode` | in-flight jobs |
| media folder | `/media` | writable: ViewDock creates library folders here, and uploads and moves write here |
| `/` | `/host` | the host's folders, for libraries outside the media folder (see below) |

### Jellyfin copies

The first time a Jellyfin title is played, ViewDock copies the original file into `/cache/jellyfin-media` while it streams from Jellyfin. Once the copy is complete, the title plays from ViewDock at its original quality, for every viewer, without Jellyfin. A copy is deleted three days after the title was last started. While someone is streaming live from the same Jellyfin server the copy downloads at up to 40 Mbps, so it does not compete with that stream. Administrators can also start a copy ahead of time with **Download to ViewDock**, or save the original file to their own device with **Download Directly**, from a Jellyfin title's right-click menu.

Copies of remuxes are large (often 30 to 80 GB). ViewDock always leaves at least 20 GB or 5% of the volume free, whichever is larger, and deletes the least recently played copies to make room; a copy started within the last 12 hours is never deleted for space. Give `/cache` a volume with room for the titles you expect to be watched within three days.

Viewers of the same Jellyfin title who stream at the same time, such as a watch party, share one Jellyfin stream; its segments are kept in `/cache/jellyfin-relay` for 20 minutes, up to 6 GB.

## User and folder permissions

The container starts as root, prepares its folders, then runs ViewDock as `PUID:PGID` (default `1000:1000`). Do not set `user:` in Compose; the entrypoint needs root for this one step.

On every start (first install, `docker compose up -d` after an update, container recreation, host reboot) the entrypoint:

- gives `/config`, `/config/uploads`, `/cache`, `/transcode` and `/update` to `PUID:PGID` (the folders themselves, not their contents);
- runs `viewdock prepare-storage`, which gives `PUID:PGID` the media root (`/media` and any `VD_LIBRARY_ROOTS`) and every folder under it, up to four levels deep, that **root** created, for example a `movies` folder made with `mkdir` over SSH. Files are never changed, and folders owned by any other account are left alone. `VD_FIX_PERMISSIONS=false` turns this off; `VD_FIX_PERMISSIONS_DEPTH` changes the depth;
- adds the group ids of `/dev/dri` devices and the Docker socket to the ViewDock account, so hardware transcoding and in-app updates keep working as a non-root user.

Library folders themselves are created by ViewDock when you add a library (**Admin → Media → Libraries**), with mode `0755` and owner `PUID:PGID`. You never need to create, `chown` or `chmod` a library folder by hand.

### Libraries in other folders of the host

A library's folder can be any folder of the host, such as a separate disk or a ZFS dataset. In **Admin → Media → Libraries**, a name like `movies` or a path in `/media` stays in ViewDock's media folder, while a full path such as `/srv/media/movies` is that folder of the host, reached through the `/host` mount. The page shows host folders as the host sees them. System folders of the host (`/etc`, `/usr`, `/proc` and the like) are refused, and **Move content** moves titles from a library in the media folder to one on another disk.

ViewDock runs as `PUID:PGID`, so it needs the same access to the host folder as that account would have: the folder owned by `PUID:PGID` (or writable by it), and every folder above it enterable. When it is not, the library page says what to run on the host, for example:

```bash
chown 1000:1000 /srv/media
```

A folder inside `/root` also needs `chmod o+x /root`, which lets other accounts pass through `/root` without listing it. Remove the `/:/host` line from `docker-compose.yml` to keep ViewDock to its media folder; installs created by the installer get the line on their next update.

## Health

`wget http://127.0.0.1:8080/healthz`. Compose `stop_grace_period` should be 60s so FFmpeg children can exit.
