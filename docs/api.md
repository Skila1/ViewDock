# ViewDock REST API

Create an API key in the admin UI, then call the JSON API with a bearer token. The full schema is in [`openapi/openapi.yaml`](https://github.com/Skila1/ViewDock/blob/main/openapi/openapi.yaml).

## Authentication

1. Open **Admin → API keys** while signed in as an administrator.
2. Create a key with a descriptive name, for example `monitoring`, and the narrowest scope that works: `admin` for full access, or `logs.read` if you only need logs.
3. Copy the `vd_…` secret. It is shown once.
4. Call the API:

```http
Authorization: Bearer vd_YOUR_SECRET
```

Cookie CSRF is not required when a `vd_` key is used.

## Useful routes

| Method | Path | Scope |
|--------|------|--------|
| GET | `/api/v1/system` | none |
| GET | `/healthz` | none |
| GET | `/api/v1/admin/logs?level=error&category=playback&limit=100` | `admin` or `logs.read` |
| GET | `/api/v1/admin/api-keys` | `admin` |
| GET | `/api/v1/admin/streams` | `admin` or `streams.inspect` |
| GET | `/api/v1/admin/stats` | `admin` or `streams.inspect` |
| GET | `/readyz` | none |
| PUT | `/api/v1/progress/{kind}/{id}` | signed-in user |
| GET, POST | `/api/v1/guests` | `users.manage`, or household owner (party-only guests) |
| DELETE | `/api/v1/guests/{id}` | issuer or `users.manage` |
| GET, PUT | `/api/v1/admin/config` | `settings.manage` |
| GET | `/api/v1/admin/config/history` | `settings.manage` |
| POST | `/api/v1/admin/config/rollback` | `settings.manage` |
| GET, POST | `/api/v1/admin/nodes` | `admin` |
| DELETE | `/api/v1/admin/nodes/{id}` | `admin` |
| PATCH | `/api/v1/admin/nodes/{id}/drain` | `admin` |
| POST | `/api/v1/admin/nodes/{id}/probe` | `admin` |
| POST | `/api/v1/admin/nodes/{id}/credential` | `admin` |
| GET | `/api/v1/admin/resilience` | `admin` |
| GET | `/api/v1/admin/resilience/sessions` | `admin` |
| GET | `/api/v1/admin/resilience/reliability` | `admin` |
| GET | `/api/v1/admin/resilience/reliability/rank?candidates=a,b&region=eu&device_class=tv` | `admin` |
| PUT, DELETE | `/api/v1/admin/resilience/reliability/overrides` | `admin` |
| POST | `/api/v1/playback/sessions/{id}/telemetry` | session owner or stream token |
| GET, POST | `/api/v1/admin/backups` | `admin` |
| POST | `/api/v1/admin/backups/destination/check` | `admin` |
| GET | `/api/v1/admin/backups/{id}/download` | `admin` |
| POST | `/api/v1/admin/backups/{id}/validate` | `admin` |
| DELETE | `/api/v1/admin/backups/{id}` | `admin` |
| GET | `/api/v1/offline/policy` | signed-in user |
| GET | `/api/v1/offline/speedtest` | signed-in user |
| GET | `/api/v1/content-restriction` | signed-in user |
| PUT, DELETE | `/api/v1/movies/{id}/rating`, `/api/v1/series/{id}/rating` | `libraries.manage` |
| GET, POST | `/api/v1/admin/households` | `users.manage` |
| GET, PATCH, DELETE | `/api/v1/admin/households/{id}` | `users.manage` |
| POST | `/api/v1/admin/households/{id}/invites` | `users.manage` |
| PATCH, DELETE | `/api/v1/admin/households/{id}/members/{userID}` | `users.manage` |
| POST | `/api/v1/admin/households/{id}/members/{userID}/move` | `users.manage` |

### Diagnostics and resilience

`GET /api/v1/admin/resilience` returns one section per component (`control`, `database`, `storage`, `coordinator`, `nodes`, `progress`, `flight_recorder`, `failovers`, `reliability`), each with `status` (`ok`, `degraded`, `down`, `unconfigured`), `error`, `checked_at`, `stale` and `data`. Sections are cached for a few seconds and time limited, so the dashboard adds no load during an outage.

`POST /api/v1/playback/sessions/{id}/telemetry` takes `{"events":[{"type","at","data"}],"correlation_id"}` with at most 50 events and 32 KiB. Accepted types: `source_selected`, `first_frame`, `startup_failed`, `stall`, `stall_end`, `buffer`, `bitrate_change`, `quality_change`, `failover`, `codec_error`, `error`, `drift_correction`, `reconnect`, `recovered`, `seek`. `data` keeps up to 16 scalar values; nested values, sensitive looking keys and URL query strings are dropped. Responses: `202 {"accepted","rejected","dropped"}`, `400`, `413`, `429` with `Retry-After` when the per-session budget (`diagnostics.telemetry_events_per_minute`) is exhausted, `410` when the session is gone. Sessions placed on a worker accept telemetry through the relay. The `X-Request-Id` of each request is recorded, and a playback session created on a worker keeps the controller's request ID.

Reliability overrides take `{"source","mode":"prefer|avoid|exclude","note"}`; node scores use sources named `node:<id>`.

### Content ratings and offline policy

`PUT /api/v1/movies/{id}/rating` takes `{"content_rating":"PG-13"}` or `{"content_rating":"Custom","rating_age":12}`; `DELETE` returns the title to TMDB lookup. `PATCH /api/v1/users/{id}` accepts `content_age_limit` (0 to 21). Titles above a viewer's limit are omitted from lists and return `404` on direct access, playback and watch party creation.

`GET /api/v1/offline/policy` returns `{enabled, max_items, expiry_days, max_item_bytes, default_allowed, libraries, issued_at}` for the caller. Session downloads (`/playback/sessions/{id}/download`) send a strong `ETag` and honour `Range` with `If-Range`, so an interrupted download resumes only when the file is unchanged.

### Media nodes

`POST /api/v1/admin/nodes` creates a node, or updates it when `id` is given. Fields: `name`, `scheme` (`http` or `https`), `host`, `port`, `role` (`media-worker`, `transcode-worker`, `storage-worker`), `region`, `capabilities`, `priority`, `weight`, `capacity`, `enabled`, `draining`. `status`, `latency_ms` and `health` are set only by the health monitor and ignored on input. Invalid or disallowed addresses return `400 invalid_endpoint`.

`POST /api/v1/admin/nodes/{id}/credential` issues a new worker secret, replacing any previous one, and returns `{"id", "secret", "env": "VD_NODE_SECRET"}` once. The secret is stored encrypted and never listed. Node list entries report `has_credential`, `failures`, `last_failure_at` and `last_error`. Every node change is written to the audit log.

When a session is placed on a worker, its `urls` point at `/mesh/{node}/...` on the same origin, the session carries `stoken` and `node` (`id`, `name`, `region`), and `urls.session` is the session's control path. Relayed requests are authorized only by `stoken`; cookies and authorization headers are not forwarded. A relayed request for a node that is down returns `410 NODE_UNAVAILABLE`. `POST /api/v1/playback/sessions` returns `503 no_worker_available` when no worker can take the session.

### Runtime configuration

`GET /api/v1/admin/config` returns `version` and `settings`. Each setting has `key`, `label`, `category`, `kind` (`string`, `url`, `int`, `bool`, `secret`, `enum`), `value`, `default`, `source` (`database`, `environment` or `default`) and `restart`. Secret values are never returned; secrets report only `set`.

`PUT /api/v1/admin/config` takes `{"version": N, "values": {"key": "value"}, "note": "..."}`. A `null` value removes the stored override so the environment or default applies again. The write is rejected with `409 version_conflict` if `version` is not the current version, and with `400 invalid_setting` (including `key`) if a value fails validation; nothing is saved in either case. Changes apply to running services immediately and to other processes sharing the database within about 5 seconds.

`GET /api/v1/admin/config/history` lists changes by version with the actor and note; secret values are masked. `POST /api/v1/admin/config/rollback` takes `{"target_version": N, "version": current}` and restores the values in effect at `target_version` as a new version.

Secrets are encrypted at rest with AES-256-GCM (see `VD_MASTER_KEY` in [environment.md](environment.md)). Saving a secret without a master key returns `503 no_master_key`.

`PUT /api/v1/progress/{kind}/{id}` takes `position_ms`, `duration_ms` and optional `force`. It returns `409 progress_conflict` when the server holds newer progress that differs by more than 30 seconds, unless `force` is true.

`POST /api/v1/guests` takes `expires_in_hours` (1 to the `guests.max_hours` setting, default 720), `max_sessions` (0 to 10, default 2), `party_only`, `library_ids`, and optional `username` and `display_name`. The generated password is returned once. Guests have no role; party-only guests can stream only titles of a watch party they joined.

### Watch parties

`POST /api/v1/watch-together/rooms` (`item_kind`, `item_id`) returns `room_id` and `invite_code`. `GET /api/v1/watch-together/invites/{code}` returns `needs_auth`, `needs_share` and, for a signed-in user or share guest, `room_id`, `item_kind` and `item_id`. `POST /api/v1/watch-together/join` takes `invite_code` (or `code`). `POST /api/v1/watch-together/rooms/{id}/ticket` returns a single-use `ticket`, the caller's `member_id` and `ws_url`, a same-origin WebSocket path valid for two minutes.

The coordinator keeps an authoritative timeline anchored to the server clock. Every `state` message carries `position_ms` as of `server_ms`; clients estimate their clock offset with `{"type":"ping","t0":<client ms>}` (answered by `pong` with `t0` and `server_ms`) and project the timeline from that. Clients report `{"type":"position","position_ms","playing","buffering","at_server_ms"}` about once a second. The coordinator answers the sender only with `{"type":"sync","action","rate","target_ms","server_ms","drift_ms"}`:

- drift within 250 ms: `ok` (rate 1);
- above 250 ms and up to the hard threshold (`sync.hard_drift_ms`, default 1000): `rate`, a playback speed between 0.95 and 1.05, held until drift falls below 120 ms so it does not oscillate;
- beyond the hard threshold: `seek` to `target_ms`, at most once every 3 seconds per member.

Buffering, paused, disconnected, unready and silent (no report for 3 s) members are neither corrected nor counted. When at least three counted members are present and two thirds of them agree with each other but are more than 250 ms off the timeline in the same direction for 3 seconds, the timeline moves to their median and a `state` with `reason: "majority"` is broadcast; the rest are then corrected individually. Host `play`, `pause` and `seek` messages (also allowed for everyone when the host sends `{"type":"settings","shared_control":true}`) take effect immediately and suspend majority alignment for 5 seconds. Other members' control messages are answered with the unchanged state. `presence` messages every 2 seconds list members with `drift_ms`, `buffering`, `eligible` and `connected`, plus `sync` counters (`reports`, `rate_corrections`, `seeks`, `realigns`). Chat messages are trimmed to 500 characters and the last 100 are kept. Rooms, queues, members and the last timeline are stored in the database and restored after a restart; rooms empty for two hours are deleted.

### Discord Activity

These routes serve the web app inside the [Discord Activity](discord.md#discord-activity). `GET /api/v1/auth/discord/activity` needs no sign-in and returns `{"enabled": false}` or `{"enabled": true, "client_id", "scopes"}`. `POST /api/v1/auth/discord/activity` takes `{"code"}` from the Embedded App SDK `authorize` command, signs in with the same rules as Discord sign-in and returns `{"access_token", "user"}`; it is limited to 10 requests a minute and returns `503 disabled` when the Activity is off, `401 token_exchange` when Discord rejects the code and `403 not_linked` or `403 denied` when the account may not sign in.

`POST /api/v1/discord/activity/room` (signed-in user) takes `instance_id` and, to start a party, `item_kind` (`movie` or `episode`) and `item_id`. ViewDock asks Discord for the Activity instance and answers only when the caller's linked Discord account is in it. It returns `{"room": {"room_id", "invite_code", "title", "created"}}` for the channel's party, or `{"room": null, "can_create"}` when there is none yet. Errors include `404 instance_not_found`, `403 not_in_activity`, `403 not_linked`, `403 party_only` and `503` when the Activity, parties or the shared bot are unavailable.

Requests carrying `X-ViewDock-Embed: discord-activity` receive session and CSRF cookies with `SameSite=None; Secure; Partitioned`. The header changes cookie attributes only. While the Activity is on, pages send `Content-Security-Policy: frame-ancestors` allowing Discord instead of `X-Frame-Options: DENY`.

### Outages

While the database is unreachable, API routes answer `503` with code `database_unavailable` and a `Retry-After` header rather than signing users out or reporting setup as incomplete. `/healthz` stays `200`; `/readyz` returns `503` and includes `database_health` (`available`, `since`, `trips`). Sign-in returns `429 session_limit` when a temporary account is at its session limit.

Example:

```bash
curl -s -H "Authorization: Bearer vd_YOUR_SECRET" \
  "https://viewdock.example.com/api/v1/admin/logs?category=playback&limit=50"
```

Operational logs keep 14 days by default (the `logs.retention_days` setting, capped in size). Tokens, `stoken` query values, and secrets are redacted before storage.
