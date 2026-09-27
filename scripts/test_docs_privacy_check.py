"""Tests for docs_privacy_check.py. Run: python -m unittest discover -s scripts -p "test_*.py"

Fake secrets are assembled at runtime so this file never contains a value the
scanner itself would flag.
"""

from __future__ import annotations

import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import docs_privacy_check as c  # noqa: E402


def findings_for(text: str, groups: list[c.Group]) -> list[str]:
    out: list[str] = []
    c.scan_text("t", text, groups, out)
    return out


class ForbiddenPaths(unittest.TestCase):
    def test_internal_documents_are_rejected(self) -> None:
        for path in [
            "AGENTS.md", "CLAUDE.md", ".cursor/rules/x.mdc", "docs/integration/vault.md",
            "docs/overhaul-progress.md", "VIEWDOCK_2_OVERHAUL.md", "notes/TODO.md", "HANDOVER.md",
            "docs/session-notes.md", "_private/list.txt",
        ]:
            with self.subTest(path=path):
                self.assertTrue(any(p.search(path) for p in c.FORBIDDEN_RE))

    def test_source_paths_are_allowed(self) -> None:
        for path in ["docs/install.md", "internal/audit/audit.go", "internal/progress/sqlite.go", "scripts/docs_privacy_check.py"]:
            with self.subTest(path=path):
                self.assertFalse(any(p.search(path) for p in c.FORBIDDEN_RE))


class Patterns(unittest.TestCase):
    groups: list[c.Group] = [("internal", c.INTERNAL_RE), ("private", c.PRIVATE_RE), ("secret", c.SECRET_RE)]

    def test_secrets_are_detected_and_truncated(self) -> None:
        samples = [
            "gh" + "p_" + "Zx9Qw" * 8,
            "AK" + "IA" + "QWERTYUIOPASDFGH",
            "-----BEGIN OPENSSH " + "PRIVATE KEY-----",
            "Bearer v" + "d_" + "k3Rt9" * 8,
            "VD_NODE_SEC" + "RET=" + "a8Kq2Lm9Zp4Xw7Rt5Yb3Nc6V",
        ]
        for sample in samples:
            with self.subTest(sample=sample[:8]):
                out = findings_for(sample, self.groups)
                self.assertEqual(len(out), 1, out)
                self.assertIn("secret:", out[0])
                self.assertNotIn(sample[8:], out[0])

    def test_placeholders_are_ignored(self) -> None:
        for sample in ["SECRET=" + "0123456789abcdef" * 2, "TOKEN=" + "x" * 32, "Bearer vd_YOUR_SECRET"]:
            with self.subTest(sample=sample):
                self.assertEqual(findings_for(sample, self.groups), [])

    def test_internal_wording_and_private_details(self) -> None:
        self.assertTrue(findings_for("Wiring the lead " + "needs to apply", self.groups))
        self.assertTrue(findings_for("Phase " + "7 status", self.groups))
        self.assertTrue(findings_for("host 192.168" + ".1.50", self.groups))
        self.assertTrue(findings_for("mail someone" + "@gmail.com", self.groups))

    def test_public_examples_pass(self) -> None:
        for sample in [
            "Set VD_PUBLIC_URL to https://viewdock.example.com",
            "Trusted ranges include 10.0.0.0/8 and 192.168.0.0/16",
            "proxy_pass http://127.0.0.1:8080;",
            "Contact admin@example.com",
        ]:
            with self.subTest(sample=sample):
                self.assertEqual(findings_for(sample, self.groups), [])


class Site(unittest.TestCase):
    def make_site(self, root: Path) -> Path:
        files, _ = c.allowlist()
        site = root / "site"
        for rel in c.expected_site_files(files):
            path = site / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("ok", encoding="utf-8")
        pages = [{"location": ""}] + [{"location": rel[:-3] + "/"} for rel in files if rel.endswith(".md") and rel != "index.md"]
        (site / "search" / "search_index.json").write_text(json.dumps({"docs": pages}), encoding="utf-8")
        return site

    def test_clean_site_passes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            out: list[str] = []
            c.check_site(self.make_site(Path(tmp)), out)
            self.assertEqual(out, [])

    def test_unexpected_pages_and_content_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            site = self.make_site(Path(tmp))
            (site / "notes").mkdir()
            (site / "notes" / "index.html").write_text("x", encoding="utf-8")
            (site / "index.html").write_text("a hand" + "over from the team", encoding="utf-8")
            (site / "install" / "index.html").write_text("token: " + "gh" + "p_" + "Zx9Qw" * 8, encoding="utf-8")
            index = json.loads((site / "search" / "search_index.json").read_text(encoding="utf-8"))
            index["docs"].append({"location": "notes/"})
            (site / "search" / "search_index.json").write_text(json.dumps(index), encoding="utf-8")
            out: list[str] = []
            c.check_site(site, out)
            text = "\n".join(out)
            self.assertIn("site/notes/index.html: unexpected file", text)
            self.assertIn("site/index.html:1: internal:", text)
            self.assertIn("site/install/index.html:1: secret:", text)
            self.assertIn("indexes unexpected page 'notes/'", text)
            shutil.rmtree(site)


if __name__ == "__main__":
    unittest.main()
