# Reverse proxy

Internet expose requires a reverse proxy or tunnel you run yourself. ViewDock does **not** ship cloudflared (or any tunnel) in Docker — configure that on the host.

ViewDock already trusts loopback, private LAN/Docker ranges, and Cloudflare edge IPs for `X-Forwarded-*`. Set `VD_PUBLIC_URL` (or Admin → Settings) to the app origin, for example `https://app.viewdock.dev`. Marketing for the project lives at `https://viewdock.dev`.

Disable buffering on `/api/v1/playback` and `/hls`. Do not cache `/api` or HLS playlists. Allow WebSockets on `/api/v1/watch-together`. Raise upload body size for offset-PUT.

## Caddy

```
app.viewdock.dev {
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

Run `cloudflared` (or any tunnel) on the host. Do not add it to the ViewDock Compose file. Point the tunnel hostname at `http://127.0.0.1:8080` (the app). Marketing is a separate site on port 8085 / `viewdock.dev`.

Pass `X-Forwarded-Proto: https`. Bypass cache for `/api` and `/hls`. Range requests must reach the origin. Set the public URL in `.env` or **Admin → Settings** so Discord OAuth and share links use a stable origin.
