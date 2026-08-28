# Changelog

## 0.1.0

- First public image on `ghcr.io/skila1/viewdock`. Tags: `latest` (default branch), the `VERSION` file, git SHA, and semver from `v*` tags.
- One-line installer writes a Compose project in `./viewdock`, installs Docker if missing, and starts the stack. Optional Cloudflare Tunnel is a systemd service, not a container.
- Admin → Updates checks GHCR digest plus GitHub `VERSION` / `CHANGELOG.md`. Update now uses the host helper (`viewdock-update`) or the Docker socket.
- Automatic updates can pull a newer image about once an hour when the helper or socket is available.
- SQLite, one container. Media stays on your disk. First-run still requires the bootstrap token from `config/setup.token` (or `VD_SETUP_TOKEN`).
