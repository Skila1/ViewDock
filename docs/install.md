# Installation

One command. A whiptail TUI writes a Docker Compose project in `./viewdock` under the current directory (`~/viewdock` from your home directory). Docker publishes port 8080. Optional Cloudflare Tunnel is the public URL. Do not `apt install docker`.

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

The wizard does not ask for an install path or a media folder. Run it from the directory that should contain `viewdock`. If that folder is already named `viewdock`, it installs in place. Cloudflared, if enabled, is a systemd service. It does not ask for an IP, a public URL, or Discord credentials.

Open `http://<host>:8080` (or your tunnel) and create the first local administrator. After ViewDock is listening it prints an 8-character setup token in the console and in `docker compose logs` (`ViewDock setup token:`). Set the public URL in `.env` or **Admin → Settings**. Upload videos under **Admin → Uploads** (10 GB max). Configure Discord and TMDB in Admin.

```bash
cd ~/viewdock
docker compose ps
docker compose logs -f
docker compose down
docker compose pull && docker compose up -d
```

Unattended:

```bash
sudo env VD_UNATTENDED=1 bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

Optional helper: `sudo viewdock status|update|logs|doctor|uninstall` (same directory).

Re-running the installer or `sudo viewdock update` on an existing folder does not overwrite `.env` values or a compose file that already has the GPU profiles. It adds missing keys (`VD_GPU`, `COMPOSE_PROFILES`, …), folds old `docker-compose.gpu.yml` / `COMPOSE_FILE` overlays into `VD_GPU=true`, and only rewrites compose when it is still the old single-service layout.

The public site is [viewdock.dev](https://viewdock.dev). A typical app hostname is `app.viewdock.dev` behind your own reverse proxy or Cloudflare Tunnel (not included in Compose).

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
