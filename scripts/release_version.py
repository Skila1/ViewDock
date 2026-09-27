"""Prepare VERSION and CHANGELOG.md for a release.

Used by .github/workflows/docker.yml on every DEPLOY: push to main:

    python scripts/release_version.py --bump --notes "Summary"   # 0.1.1 -> 0.1.2, 0.1.9 -> 0.2.0
    python scripts/release_version.py --notes "Summary"          # VERSION was set by hand

The changelog gets a "## <version>" heading: an existing "## Unreleased"
section is renamed, otherwise a new section is added with the notes. Prints
the release version.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SEMVER_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)$")
UNRELEASED_RE = re.compile(r"^## +Unreleased *$", re.IGNORECASE | re.MULTILINE)
DEFAULT_NOTE = "Maintenance release."


def parse_version(text: str) -> tuple[int, int, int]:
    match = SEMVER_RE.match(text.strip())
    if not match:
        raise ValueError(f"VERSION must be MAJOR.MINOR.PATCH, got {text.strip()!r}")
    major, minor, patch = (int(p) for p in match.groups())
    return major, minor, patch


def next_patch(text: str) -> str:
    """Patch numbers run 0 to 9; after .9 the minor version goes up (0.1.9 -> 0.2.0)."""
    major, minor, patch = parse_version(text)
    if patch >= 9:
        return f"{major}.{minor + 1}.0"
    return f"{major}.{minor}.{patch + 1}"


def release_changelog(text: str, version: str, notes: str) -> str:
    if re.search(rf"^## +v?{re.escape(version)} *$", text, re.MULTILINE):
        return text
    if UNRELEASED_RE.search(text):
        return UNRELEASED_RE.sub(f"## {version}", text, count=1)
    section = f"## {version}\n\n- {notes.strip() or DEFAULT_NOTE}\n\n"
    first = re.search(r"^## ", text, re.MULTILINE)
    if first:
        return text[: first.start()] + section + text[first.start() :]
    return text.rstrip("\n") + "\n\n" + section.rstrip("\n") + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--bump", action="store_true", help="increase the patch version first")
    parser.add_argument("--notes", default="", help="changelog note when there is no Unreleased section")
    parser.add_argument("--root", type=Path, default=ROOT, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    version_file = args.root / "VERSION"
    changelog_file = args.root / "CHANGELOG.md"
    try:
        current = version_file.read_text(encoding="utf-8")
        version = next_patch(current) if args.bump else ".".join(str(p) for p in parse_version(current))
    except (OSError, ValueError) as err:
        print(f"release_version: {err}", file=sys.stderr)
        return 1

    if args.bump:
        version_file.write_text(version + "\n", encoding="utf-8", newline="\n")
    changelog = changelog_file.read_text(encoding="utf-8") if changelog_file.exists() else "# Changelog\n"
    updated = release_changelog(changelog, version, args.notes)
    if updated != changelog:
        changelog_file.write_text(updated, encoding="utf-8", newline="\n")
    print(version)
    return 0


if __name__ == "__main__":
    sys.exit(main())
