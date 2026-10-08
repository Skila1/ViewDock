"""Raise the Windows app's version when a release changes the app.

Used by .github/workflows/docker.yml on every DEPLOY: push to main, beside
release_version.py:

    python scripts/desktop_version.py --since <commit before the push>

When anything under desktop/ changed since that commit and desktop/VERSION
did not, the patch version goes up the way ViewDock's does (1.0.1 -> 1.0.2,
1.0.9 -> 1.1.0) and desktop/app/package.json follows. Installed apps update
to a new version the next time they start. Prints the app version.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from release_version import next_patch, parse_version  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent


def changed(root: Path, since: str) -> list[str]:
    out = subprocess.run(
        ["git", "diff", "--name-only", since, "HEAD", "--", "desktop/"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [line.strip() for line in out.splitlines() if line.strip()]


def set_package_version(root: Path, version: str) -> None:
    pkg_file = root / "desktop" / "app" / "package.json"
    pkg = json.loads(pkg_file.read_text(encoding="utf-8"))
    if pkg.get("version") == version:
        return
    pkg["version"] = version
    pkg_file.write_text(json.dumps(pkg, indent=2) + "\n", encoding="utf-8", newline="\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--since", required=True, help="the commit the release starts from")
    parser.add_argument("--root", type=Path, default=ROOT, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    version_file = args.root / "desktop" / "VERSION"
    try:
        current = ".".join(str(p) for p in parse_version(version_file.read_text(encoding="utf-8")))
        files = changed(args.root, args.since)
    except (OSError, ValueError, subprocess.CalledProcessError) as err:
        print(f"desktop_version: {err}", file=sys.stderr)
        return 1

    version = current
    if files and "desktop/VERSION" not in files:
        version = next_patch(current)
        version_file.write_text(version + "\n", encoding="utf-8", newline="\n")
    set_package_version(args.root, version)
    print(version)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
