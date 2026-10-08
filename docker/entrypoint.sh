#!/bin/sh
# ViewDock container entrypoint.
#
# The container starts as root only long enough to prepare the folders
# ViewDock writes to, then runs ViewDock itself as PUID:PGID (1000:1000 by
# default). Nothing here needs to be repeated by hand: container recreation,
# host reboots and image updates all pass through this script again.
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

  # Service folders: the folder inodes only — never walk HLS trees here.
  mkdir -p /config/uploads
  for d in /config /config/uploads /cache /transcode /update; do
    [ -d "$d" ] || continue
    chown "$PUID:$PGID" "$d" 2>/dev/null || true
    chmod u+rwx "$d" 2>/dev/null || true
  done

  # Media storage (VD_MEDIA_DIR and VD_LIBRARY_ROOTS): give ViewDock the
  # storage roots and any library, show or season folder that root created
  # outside ViewDock (mkdir over SSH). Folders only; media files and folders
  # owned by other accounts are never changed. VD_FIX_PERMISSIONS=false
  # turns this off.
  /usr/local/bin/viewdock prepare-storage || echo "prepare-storage failed; continuing" >&2

  # Supplementary groups for GPU render nodes and the Docker socket, so
  # hardware transcoding and in-app updates keep working after the drop.
  add_gid() {
    gid="$1"
    [ -n "$gid" ] && [ "$gid" != "0" ] || return 0
    name="$(getent group "$gid" | cut -d: -f1 || true)"
    if [ -z "$name" ]; then
      name="vdgid$gid"
      groupadd -g "$gid" "$name" 2>/dev/null || return 0
    fi
    usermod -aG "$name" viewdock 2>/dev/null || true
  }
  if [ -d /dev/dri ]; then
    for n in /dev/dri/*; do
      [ -e "$n" ] || continue
      add_gid "$(stat -c '%g' "$n" 2>/dev/null || true)"
    done
  fi
  if [ -S /var/run/docker.sock ]; then
    add_gid "$(stat -c '%g' /var/run/docker.sock 2>/dev/null || true)"
  fi

  # Run as the named account so its supplementary groups apply; fall back
  # to the numeric ids if the account could not be remapped.
  if [ "$(id -u viewdock 2>/dev/null || true)" = "$PUID" ]; then
    exec gosu viewdock "$@"
  fi
  exec gosu "${PUID}:${PGID}" "$@"
fi

exec "$@"
