---
description: Install, configure and run ViewDock, the self-hosted video platform for movies and TV.
hide:
  - navigation
  - toc
---

<div class="vd-hero" markdown>

![ViewDock](assets/logo.svg)

# ViewDock Docs

<p class="vd-tagline">Mount a folder or upload a video. ViewDock handles the rest.</p>

ViewDock is a private, self-hosted video platform for movies and TV. Stream the files you already own to any browser, watch together in sync, and download titles for offline viewing. Your media stays on your disk and is never modified.

[Get started](install.md){ .md-button .md-button--primary }
[View on GitHub](https://github.com/Skila1/ViewDock){ .md-button }

</div>

## Install

Run the installer on a Linux host. It sets up Docker if needed, writes a Docker Compose project and starts ViewDock on port 8080.

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Skila1/ViewDock/main/install.sh)"
```

## Getting started

1. **Install ViewDock.** Run the command above from the directory that should contain the `viewdock` folder. For scripted installs, see [unattended installation](install.md).
2. **Create your administrator.** Open `http://<host>:8080`, enter the setup token printed in the console and in `docker compose logs`, and create the first administrator account.
3. **Add your media.** Mount your movie and TV folders, or upload videos under **Admin → Uploads**.
4. **Make it reachable.** Set your public URL under **Admin → Settings** and put ViewDock behind a [reverse proxy or Cloudflare Tunnel](reverse-proxy.md).
5. **Keep it safe and current.** Schedule [backups](backups.md) and [upgrade](upgrade.md) with a single command.

## Explore the docs

<div class="grid cards" markdown>

-   :material-rocket-launch-outline:{ .lg .middle } **Getting started**

    ---

    Install ViewDock, run it with Docker, put it behind a reverse proxy and keep it up to date.

    [:octicons-arrow-right-24: Installation](install.md) ·
    [Docker](docker.md) ·
    [Reverse proxy](reverse-proxy.md) ·
    [Upgrade](upgrade.md)

-   :material-tune-variant:{ .lg .middle } **Configuration**

    ---

    Environment variables, Discord sign-in and bot, browser uploads, and scheduled backups to local disk or S3-compatible storage.

    [:octicons-arrow-right-24: Environment](environment.md) ·
    [Discord](discord.md) ·
    [Uploads](uploads.md) ·
    [Backups and restore](backups.md)

-   :material-api:{ .lg .middle } **API reference**

    ---

    Automate ViewDock with API keys and the REST API.

    [:octicons-arrow-right-24: REST API](api.md)

-   :material-source-branch:{ .lg .middle } **Development**

    ---

    Build from source, understand the repository layout, run the tests and contribute.

    [:octicons-arrow-right-24: Development](development.md)

</div>

## Get help

- Source code and issues: [github.com/Skila1/ViewDock](https://github.com/Skila1/ViewDock)
- Project website: [viewdock.dev](https://viewdock.dev)

ViewDock is licensed under the GNU Affero General Public License v3.0 or later.
