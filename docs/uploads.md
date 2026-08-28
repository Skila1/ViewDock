# Uploads

Administrators upload videos in the web app under **Admin → Uploads**. Ordinary users and share guests cannot upload. Maximum size is **10 GiB** per file. Only video extensions are accepted (mkv, mp4, avi, m4v, mov, ts, m2ts, wmv, webm, mpg, mpeg, flv).

## How it works

1. The browser creates an upload session (`POST /api/v1/uploads`) with library, filename, and size.
2. Bytes are sent with resumable offset PUTs (8 MiB chunks). Wrong offsets return `409` and the server’s current offset.
3. Staging files live in `$VD_CONFIG_DIR/uploads` (Docker: `/config/uploads`). They are not inside the media library.
4. When the declared size is reached, ViewDock fsyncs, probes with ffprobe, then moves the file into the library. Cross-filesystem moves use copy + fsync + rename. Existing names get `Title (2).mkv` — nothing is overwritten.
5. The normal ingest/scan path catalogues the file. Partial staging files are never treated as completed media.

Abandoned `open` sessions expire after 24 hours of inactivity. Completed, failed, and cancelled rows are removed after 7 days. Staging for those failed/cancelled rows is deleted; completed media stays in the library.

## Compose

`/media` must be **read-write**. A leftover `:ro` mount will fail when the file is placed. `viewdock doctor` reports that.

Uploads never recursively chown the media folder.

## Reverse proxies and Cloudflare

ViewDock streams each PUT to disk (32 KiB buffer). Do not buffer request bodies on `/api/v1/uploads`. Cloudflare Tunnel and orange-cloud have a **per-request** body limit (often 100 MB). 8 MiB chunks stay under that. A single huge PUT would fail at the proxy, not in ViewDock.

HTTP read/write timeouts on the app are unset so a 10 GB transfer is not killed mid-chunk.
