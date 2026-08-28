# syntax=docker/dockerfile:1

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm \
    if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else echo "no web lockfile"; fi
COPY web/ ./
RUN --mount=type=cache,target=/root/.npm \
    if [ -f package.json ] && grep -q '"build"' package.json; then npm run build; else mkdir -p dist && printf '%s\n' '<!doctype html><title>ViewDock</title><p>ViewDock</p>' > dist/index.html; fi

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY openapi ./openapi
COPY web/embed.go ./web/embed.go
COPY VERSION ./
COPY --from=web /src/web/dist ./web/dist
ARG VERSION
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ver="${VERSION}"; \
    if [ -z "${ver}" ] && [ -f VERSION ]; then ver="$(tr -d '[:space:]' < VERSION)"; fi; \
    if [ -z "${ver}" ]; then ver=0.1.0; fi; \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X github.com/viewdock/viewdock/internal/version.Version=${ver}" -o /out/viewdock ./cmd/viewdock

FROM debian:bookworm-slim
ARG TARGETARCH
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-cache \
    && apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates ffmpeg wget libzimg2 gosu \
    && if [ "$TARGETARCH" = "amd64" ]; then \
         apt-get install -y --no-install-recommends vainfo intel-media-va-driver-non-free || \
         apt-get install -y --no-install-recommends vainfo intel-media-va-driver || true; \
       fi \
    && useradd --system --home /config --uid 1000 viewdock \
    && mkdir -p /config /cache /transcode /media \
    && chown viewdock:viewdock /config /cache /transcode
COPY --from=build /out/viewdock /usr/local/bin/viewdock
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENV VD_HTTP_ADDR=:8080 \
    VD_CONFIG_DIR=/config \
    VD_CACHE_DIR=/cache \
    VD_TRANSCODE_DIR=/transcode \
    VD_MEDIA_DIR=/media
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --retries=8 CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["viewdock"]
