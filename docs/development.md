# Development

ViewDock is a Go server with a React web app, licensed under the PolyForm Noncommercial License 1.0.0. This page covers building it from source, the repository layout, tests and how to contribute.

## Requirements

- Go 1.25 or later
- Node.js 24 (the version the Docker image builds with) and npm
- FFmpeg and ffprobe on `PATH` for remux, transcode and accurate probes. The filename catalogue and Direct Play work without them.
- Docker, for container builds and the end-to-end tests

## Build and run

```bash
cd web && npm install && npm run build && cd ..
go run ./cmd/viewdock
```

Open `http://127.0.0.1:8080` and follow the first-run setup. For the web app with hot reload, run `npm run dev` in `web/` alongside the server.

To build and run the container image from your checkout:

```bash
cp .env.example .env
docker compose up -d --build
```

## Repository layout

| Path | Contents |
|------|----------|
| `cmd/viewdock` | Server entry point, runtime configuration definitions and service wiring |
| `internal/` | Server packages, one per area (see below) |
| `migrations/` | SQL schema migrations, applied forward-only on startup |
| `web/` | React and TypeScript web app (Vite, Tailwind, Vitest) |
| `openapi/openapi.yaml` | REST API schema |
| `docker/` | Dockerfile and container entrypoint |
| `desktop/` | ViewDock for Windows: the Electron app and the script that prepares it (see [Windows app](desktop.md#development)) |
| `docker-compose.yml` | Production Compose file, including the optional GPU, distributed, mesh and coordinator profiles |
| `install.sh`, `installer/` | One-line installer and host helper |
| `test/e2e/` | End-to-end tests that drive a running server over HTTP |
| `scripts/acceptance/` | Helpers for end-to-end runs, such as generating test media |
| `docs/` | Source of this documentation site |

Main server packages in `internal/`:

| Area | Packages |
|------|----------|
| HTTP and sessions | `httpapi`, `session`, `auth`, `users`, `setup` |
| Library and metadata | `library`, `scan`, `media`, `metadata`, `artwork`, `search`, `collections`, `subtitle` |
| Playback | `playback`, `decision`, `capability`, `ffmpeg`, `hwaccel`, `transcode`, `hls`, `progress`, `bandwidth`, `download` |
| Sharing and parties | `share`, `watchtogether`, `discordbot` |
| Storage and data | `db`, `storage`, `backup`, `upload`, `cache`, `secrets` |
| Multi-server | `backend` (node registry and placement), `mesh`, `nodeauth`, `reliability`, `resilience` |
| Operations | `runtimecfg`, `config`, `settings`, `audit`, `oplog`, `log`, `diagnostics`, `inspector`, `update`, `installcompose`, `version`, `desktop` |

## Tests

```bash
go vet ./...
go test ./...
cd web && npm test && npm run build
```

`npm run build` also type-checks the web app.

The end-to-end tests use the `e2e` build tag and run against a disposable ViewDock instance with generated test media. They create accounts and change settings, so never point them at a server you use.

```bash
VD_E2E_URL=http://127.0.0.1:18080 VD_E2E_SETUP_TOKEN=<setup token> \
  go test -tags e2e ./test/e2e -count=1 -v
```

`VD_E2E_SETUP_TOKEN` is the token a fresh instance prints on first start. `VD_E2E_ADMIN_USER`, `VD_E2E_ADMIN_PASS` and `VD_E2E_MEDIA_DIR` override the test administrator and media path. `scripts/acceptance/make-media.sh` generates small test-pattern movies inside a container.

## Documentation

This site is built with [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) from `docs/`:

```bash
pip install -r requirements-docs.txt
mkdocs serve
```

Every page must be listed in both `nav` and the `exclude_docs` allowlist in `mkdocs.yml`; anything else in `docs/` is left out of the build. Before committing documentation changes, run:

```bash
python -m unittest discover -s scripts -p "test_*.py"
python scripts/docs_privacy_check.py
mkdocs build --strict
python scripts/docs_privacy_check.py --site site
```

The privacy check rejects internal documents, unlisted pages, personal paths and addresses, and credentials in tracked files and in the built site. CI runs the same checks on every push and pull request, and the site is deployed only when they pass.

The wiki documents released functionality and supported developer instructions only. Keep working notes, implementation plans, progress reports, internal audits and AI agent context out of the repository and out of `docs/`. Examples must use placeholders such as `viewdock.example.com`, never real hostnames, addresses, tokens or configuration from a running instance.

## Contributing

1. Fork the repository and create a branch.
2. Keep changes focused, and follow the style of the surrounding code.
3. Run the tests above, and update the documentation when you change user-facing behaviour, configuration or the API.
4. Open a pull request describing what changed and why.
