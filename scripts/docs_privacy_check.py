#!/usr/bin/env python3
"""Keeps internal material out of the public repository and documentation site.

    python scripts/docs_privacy_check.py              check tracked files and doc sources
    python scripts/docs_privacy_check.py --site site  also check a built MkDocs site

The list of published documentation files is the exclude_docs allowlist in
mkdocs.yml.

Exits non-zero and prints every finding when a check fails.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Tracked paths that must never be in the public repository: agent context,
# working notes, plans, progress reports and handovers.
FORBIDDEN_PATHS = [
    r"(^|/)AGENTS\.md$",
    r"(^|/)CLAUDE\.md$",
    r"(^|/)GEMINI\.md$",
    r"(^|/)\.cursorrules$",
    r"(^|/)\.cursor/",
    r"(^|/)\.claude/",
    r"(^|/)copilot-instructions\.md$",
    r"(^|/)_private/",
    r"VIEWDOCK_\d",
    r"overhaul",
    r"hand-?(over|off)",
    r"(^|/)docs/integration/",
    r"progress\.md$",
    r"(^|/)(todo|notes|plan|scratch|session|journal|devlog)[^/]*\.(md|txt)$",
    r"implementation[-_ ]?plan",
    r"session[-_ ]?notes",
    r"dev[-_ ]?log",
    r"internal[-_ ]audit",
    r"source[-_ ]of[-_ ]truth",
]

# Wording that belongs to internal development records, not public guides.
INTERNAL_TEXT = [
    r"overhaul",
    r"hand-?(over|off)\b",
    r"\bsub-?agents?\b",
    r"\bagent (flow|session|notes?|prompts?)\b",
    r"\bthe lead\b",
    r"source of truth",
    r"\bphase \d",
    r"acceptance tests? \d",
    r"needs to apply",
    r"not wired",
    r"\bAGENTS\.md\b",
    r"\bCLAUDE\.md\b",
    r"VIEWDOCK_\d",
    r"cursor-debug",
    r"\.cursor/",
    r"coding session",
]

# Personal paths, private network addresses and personal email addresses.
PRIVATE_TEXT = [
    r"[A-Za-z]:\\Users\\",
    r"/home/[a-z_][\w-]*/",
    r"/Users/[A-Za-z][\w-]*/",
    r"(?<![\d.])(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})(?![\d/])",
    r"[\w.+-]+@(?!(?:[\w-]+\.)*(?:example\.(?:com|org|net)|users\.noreply\.github\.com)\b)[\w-]+(?:\.[\w-]+)*\.[a-z]{2,}\b",
]

# Credentials and tokens.
SECRET_TEXT = [
    r"\bgh[pousr]_[A-Za-z0-9]{30,}",
    r"\bgithub_pat_[A-Za-z0-9_]{20,}",
    r"\bAKIA[0-9A-Z]{16}\b",
    r"-----BEGIN [A-Z ]*PRIVATE KEY-----",
    r"\bxox[abprs]-[A-Za-z0-9-]{10,}",
    r"\b[rs]k_live_[A-Za-z0-9]{10,}",
    r"\b[MNO][A-Za-z\d_-]{23,25}\.[\w-]{6}\.[\w-]{27,}",
    r"\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.",
    r"\bvd_[A-Za-z0-9_-]{30,}",
    r"(?i)\b[\w]*(?:secret|token|password|passwd|api_key|master_key)\s*[=:]\s*['\"]?[A-Za-z0-9+/_\-]{20,}",
]

# Public text outside docs/ that also gets the documentation checks.
EXTRA_SOURCES = ["README.md", "CHANGELOG.md", "mkdocs.yml", ".env.example"]

SITE_FIXED = {"404.html", "sitemap.xml", "sitemap.xml.gz", "search/search_index.json"}
TEXT_SUFFIXES = {".md", ".html", ".json", ".xml", ".css", ".js", ".txt", ".yml", ".yaml", ".svg", ""}
MAX_SCAN_BYTES = 5 * 1024 * 1024


def compile_all(patterns: list[str]) -> list[re.Pattern[str]]:
    return [re.compile(p, re.IGNORECASE) for p in patterns]


FORBIDDEN_RE = compile_all(FORBIDDEN_PATHS)
INTERNAL_RE = compile_all(INTERNAL_TEXT)
PRIVATE_RE = compile_all(PRIVATE_TEXT)
SECRET_RE = [re.compile(p) for p in SECRET_TEXT]


Group = tuple[str, list[re.Pattern[str]]]

PLACEHOLDER_SEQUENCES = ("0123456789abcdefghijklmnopqrstuvwxyz" * 4, "0123456789abcdef" * 8)


def is_placeholder(value: str) -> bool:
    """Obvious dummy values in tests and examples, such as 0123456789abcdef or xxxx."""
    tail = re.split(r"[=:'\"\s]+", value)[-1].lower()
    return len(set(tail)) <= 3 or (len(tail) >= 16 and any(tail in s for s in PLACEHOLDER_SEQUENCES))


def scan_text(label: str, text: str, groups: list[Group], findings: list[str]) -> None:
    for number, line in enumerate(text.splitlines(), 1):
        for kind, patterns in groups:
            for pattern in patterns:
                match = pattern.search(line)
                if not match:
                    continue
                if kind == "secret":
                    if is_placeholder(match.group(0)):
                        continue
                    findings.append(f"{label}:{number}: secret: {match.group(0)[:6]}...")
                else:
                    findings.append(f"{label}:{number}: {kind}: {match.group(0)}")


def read_text(path: Path) -> str | None:
    """Returns the file as text, or None for binary or very large files."""
    try:
        if path.stat().st_size > MAX_SCAN_BYTES:
            return None
        data = path.read_bytes()
    except OSError:
        return None
    if b"\0" in data[:8192]:
        return None
    return data.decode("utf-8", errors="replace")


def allowlist() -> tuple[set[str], list[str]]:
    """Returns (published files, theme globs) from exclude_docs in mkdocs.yml."""
    text = (ROOT / "mkdocs.yml").read_text(encoding="utf-8")
    block = re.search(r"^exclude_docs:\s*\|\s*\n((?:[ \t]+.*\n?)+)", text, re.MULTILINE)
    if not block:
        sys.exit("mkdocs.yml has no exclude_docs allowlist")
    lines = [line.strip() for line in block.group(1).splitlines() if line.strip()]
    if lines[0] != "*":
        sys.exit("exclude_docs must start with '*' so only allowlisted files are published")
    files: set[str] = set()
    globs: list[str] = []
    for line in lines[1:]:
        if not line.startswith("!/"):
            sys.exit(f"unexpected exclude_docs entry: {line}")
        entry = line[2:]
        if "*" in entry:
            globs.append(entry)
        else:
            files.add(entry)
    return files, globs


def tracked_files() -> list[str]:
    out = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True)
    return [p for p in out.stdout.decode("utf-8").split("\0") if p]


def check_sources(findings: list[str]) -> None:
    files, globs = allowlist()
    tracked = tracked_files()

    for path in tracked:
        for pattern in FORBIDDEN_RE:
            if pattern.search(path):
                findings.append(f"{path}: internal document must not be tracked ({pattern.pattern})")
                break

    for path in tracked:
        if not path.startswith("docs/"):
            continue
        rel = path[len("docs/"):]
        if rel not in files:
            findings.append(f"{path}: not in the mkdocs.yml exclude_docs allowlist")
        elif any(fnmatch.fnmatch(rel, g) for g in globs):
            findings.append(f"{path}: collides with a theme asset path")

    for rel in sorted(files):
        if not (ROOT / "docs" / rel).is_file():
            findings.append(f"docs/{rel}: allowlisted but missing")

    docs = {f"docs/{rel}" for rel in files if Path(rel).suffix in TEXT_SUFFIXES} | set(EXTRA_SOURCES)
    doc_groups: list[Group] = [("internal", INTERNAL_RE), ("private", PRIVATE_RE), ("secret", SECRET_RE)]
    repo_groups: list[Group] = [("secret", SECRET_RE)]
    for rel in sorted(set(tracked) | docs):
        text = read_text(ROOT / rel)
        if text is not None:
            scan_text(rel, text, doc_groups if rel in docs else repo_groups, findings)


def expected_site_files(files: set[str]) -> set[str]:
    expected = set(SITE_FIXED)
    for rel in files:
        if rel.endswith(".md"):
            stem = rel[:-3]
            expected.add("index.html" if stem == "index" else f"{stem}/index.html")
        else:
            expected.add(rel)
    return expected


def check_site(site: Path, findings: list[str]) -> None:
    if not (site / "index.html").is_file():
        findings.append(f"{site}: no built site found")
        return
    files, globs = allowlist()
    expected = expected_site_files(files)
    full: list[Group] = [("internal", INTERNAL_RE), ("private", PRIVATE_RE), ("secret", SECRET_RE)]
    theme_groups: list[Group] = [("secret", SECRET_RE)]
    for path in sorted(p for p in site.rglob("*") if p.is_file()):
        rel = path.relative_to(site).as_posix()
        theme = any(fnmatch.fnmatch(rel, g) for g in globs)
        if rel not in expected and not theme:
            findings.append(f"site/{rel}: unexpected file in the built site")
            continue
        if path.suffix not in TEXT_SUFFIXES:
            continue
        text = read_text(path)
        if text is not None:
            scan_text(f"site/{rel}", text, theme_groups if theme else full, findings)
    for rel in sorted(expected - {"sitemap.xml.gz"}):
        if not (site / rel).is_file():
            findings.append(f"site/{rel}: expected but missing from the build")

    index = site / "search" / "search_index.json"
    if index.is_file():
        pages = {rel[: -len("index.html")] for rel in expected if rel.endswith("index.html")}
        for doc in json.loads(index.read_text(encoding="utf-8")).get("docs", []):
            page = doc.get("location", "").split("#", 1)[0]
            if page not in pages:
                findings.append(f"site/search/search_index.json: indexes unexpected page {page!r}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--site", type=Path, help="built MkDocs site directory to scan")
    args = parser.parse_args()

    findings: list[str] = []
    check_sources(findings)
    if args.site:
        check_site(args.site if args.site.is_absolute() else ROOT / args.site, findings)

    if findings:
        print("Documentation privacy check failed:", file=sys.stderr)
        for finding in findings:
            print(f"  {finding}", file=sys.stderr)
        return 1
    print("Documentation privacy check passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
