#!/bin/sh
# Generates small, license-free FFmpeg test-pattern movies inside a ViewDock
# container so acceptance runs have real media to scan, probe and transcode.
#   docker exec <container> sh /scripts/make-media.sh /media
set -eu
dir="${1:-/media}"
mkdir -p "$dir"
make() {
  name="$1"; secs="$2"; size="$3"; tone="$4"
  out="$dir/$name/$name.mp4"
  [ -f "$out" ] && return 0
  mkdir -p "$dir/$name"
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "testsrc2=duration=${secs}:size=${size}:rate=24" \
    -f lavfi -i "sine=frequency=${tone}:duration=${secs}" \
    -c:v libx264 -preset veryfast -pix_fmt yuv420p -g 48 -c:a aac -b:a 96k \
    -movflags +faststart "$out"
}
make "Test Pattern Comedy (2004)" 180 1280x720 440
make "Signal Drama (1999)" 120 854x480 660
