#!/usr/bin/env bash
# Thin wrapper. The one-click installer lives at the repo root.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
exec bash "${here}/install.sh" "$@"
