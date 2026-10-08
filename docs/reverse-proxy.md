# Reverse proxy

Exposing ViewDock to the internet requires a reverse proxy or tunnel you run yourself. ViewDock does **not** ship cloudflared (or any tunnel) in Docker; configure that on the host.

ViewDock already trusts loopback, private LAN/Docker ranges, and Cloudflare edge IPs for `X-Forwarded-*`. `X-Forwarded-For`, `X-Real-IP` and `True-Client-IP` are only honoured from those peers, so clients connecting directly cannot choose the IP used for rate limits and audit logs. Set `VD_PUBLIC_URL` (or Admin → Settings) to the app origin, for example `https://viewdock.example.com`.

Disable buffering on `/api/v1/playback`, `/api/v1/uploads`, and `/hls`. Do not cache `/api` or HLS playlists. Allow WebSockets on `/api/v1/watch-together`. Watch-party WebSocket handshakes accept only the configured public application origin; set `VD_PUBLIC_URL` or Admin → Settings to the exact browser-facing origin, including scheme and port when applicable. Distributed frontends may use the comma-separated `VD_ALLOWED_ORIGINS` setting. Requests without an `Origin` header or from another origin are rejected. Admin uploads use 8 MiB offset PUTs (10 GiB max per file). Cloudflare’s per-request body limit still applies to each PUT, not the whole file.

## Caddy

```
viewdock.example.com {
  reverse_proxy 127.0.0.1:8080 {
    flush_interval -1
  }
}
```

## nginx

```
location / {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $connection_upgrade;
  proxy_buffering off;
  proxy_request_buffering off;
  client_max_body_size 0;
  proxy_read_timeout 3600s;
}
```

## Traefik

Forward `X-Forwarded-*`. Disable buffering on playback/HLS routers. Enable WebSockets.

## Cloudflare Tunnel

Run `cloudflared` (or any tunnel) on the host. Do not add it to the ViewDock Compose file. Point the tunnel hostname at `http://127.0.0.1:8080` (the app).

Pass `X-Forwarded-Proto: https`. Bypass cache for `/api` and `/hls`. Range requests must reach the origin. Set the public URL in `.env` or **Admin → Settings** so Discord OAuth and share links use a stable origin.
