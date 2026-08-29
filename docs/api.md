# ViewDock REST API

Same idea as SoundDock: create an API key in the admin UI, then call the JSON API with a bearer token.

## Agent flow

1. Open **Admin → API keys** while signed in as an administrator.
2. Create a key named e.g. `cursor-debug` with the `admin` scope (or `logs.read` if you only need logs).
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

Example:

```bash
curl -s -H "Authorization: Bearer vd_YOUR_SECRET" \
  "https://app.viewdock.dev/api/v1/admin/logs?category=playback&limit=50"
```

Operational logs keep about 14 days (capped). Tokens, `stoken` query values, and secrets are redacted before storage.
