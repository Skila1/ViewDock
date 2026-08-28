#!/bin/sh
set -eu

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -u)" = "0" ]; then
  if ! getent group viewdock >/dev/null 2>&1; then
    groupadd -g "$PGID" viewdock 2>/dev/null || groupmod -g "$PGID" viewdock
  else
    groupmod -g "$PGID" viewdock 2>/dev/null || true
  fi
  usermod -u "$PUID" -g "$PGID" viewdock 2>/dev/null || true

  # Directory inodes only — never walk HLS trees, never chown /media.
  chown "$PUID:$PGID" /config /cache /transcode 2>/dev/null || true

  extra=""
  if [ -d /dev/dri ]; then
    for n in /dev/dri/*; do
      [ -e "$n" ] || continue
      gid=$(stat -c '%g' "$n" 2>/dev/null || true)
      if [ -n "${gid:-}" ] && [ "$gid" != "0" ]; then
        extra="${extra},${gid}"
      fi
    done
  fi
  extra="${extra#,}"
  if [ -n "$extra" ]; then
    exec gosu "${PUID}:${PGID}${extra:+,$extra}" "$@"
  fi
  exec gosu "${PUID}:${PGID}" "$@"
fi

exec "$@"
