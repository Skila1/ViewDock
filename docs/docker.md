# Docker

The Phase 0 image is Debian + ffmpeg + libzimg (`zscale`). VAAPI packages install on `linux/amd64` only.

- Image: `ghcr.io/viewdock/viewdock:latest`
- Platforms: `linux/amd64`, `linux/arm64`
- NVIDIA / jellyfin-ffmpeg overlay is a later image (`:nvidia`), not required to start

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
