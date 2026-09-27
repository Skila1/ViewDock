"""Tests for release_version.py. Run: python -m unittest discover -s scripts -p "test_*.py" """

from __future__ import annotations

import contextlib
import io
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import release_version as r  # noqa: E402


class VersionTests(unittest.TestCase):
    def test_next_patch(self) -> None:
        self.assertEqual(r.next_patch("0.1.1\n"), "0.1.2")
        self.assertEqual(r.next_patch("1.9.9"), "1.9.10")

    def test_rejects_non_semver(self) -> None:
        for bad in ("", "0.1", "v0.1.1", "0.1.1-beta", "latest"):
            with self.assertRaises(ValueError, msg=bad):
                r.next_patch(bad)


class ChangelogTests(unittest.TestCase):
    def test_renames_unreleased(self) -> None:
        text = "# Changelog\n\n## Unreleased\n\n- New thing.\n\n## 0.1.1\n\n- Old.\n"
        out = r.release_changelog(text, "0.1.2", "ignored")
        self.assertEqual(out, "# Changelog\n\n## 0.1.2\n\n- New thing.\n\n## 0.1.1\n\n- Old.\n")

    def test_adds_section_with_notes(self) -> None:
        text = "# Changelog\n\n## 0.1.1\n\n- Old.\n"
        out = r.release_changelog(text, "0.1.2", "Fix the thing")
        self.assertEqual(out, "# Changelog\n\n## 0.1.2\n\n- Fix the thing\n\n## 0.1.1\n\n- Old.\n")

    def test_default_note_and_no_sections(self) -> None:
        out = r.release_changelog("# Changelog\n", "0.1.0", "  ")
        self.assertEqual(out, "# Changelog\n\n## 0.1.0\n\n- Maintenance release.\n")

    def test_existing_heading_is_left_alone(self) -> None:
        text = "# Changelog\n\n## 0.1.2\n\n- Written by hand.\n\n## Unreleased\n"
        self.assertEqual(r.release_changelog(text, "0.1.2", "x"), text)


class MainTests(unittest.TestCase):
    def run_main(self, root: Path, *args: str) -> tuple[int, str]:
        out = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            code = r.main(["--root", str(root), *args])
        return code, out.getvalue().strip()

    def test_bump_writes_both_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "VERSION").write_text("0.1.1\n", encoding="utf-8")
            (root / "CHANGELOG.md").write_text("# Changelog\n\n## Unreleased\n\n- New.\n", encoding="utf-8")
            code, printed = self.run_main(root, "--bump", "--notes", "n")
            self.assertEqual((code, printed), (0, "0.1.2"))
            self.assertEqual((root / "VERSION").read_text(encoding="utf-8"), "0.1.2\n")
            self.assertIn("## 0.1.2\n", (root / "CHANGELOG.md").read_text(encoding="utf-8"))

    def test_manual_version_is_kept(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "VERSION").write_text("0.2.0\n", encoding="utf-8")
            (root / "CHANGELOG.md").write_text("# Changelog\n", encoding="utf-8")
            code, printed = self.run_main(root, "--notes", "Big one")
            self.assertEqual((code, printed), (0, "0.2.0"))
            self.assertEqual((root / "VERSION").read_text(encoding="utf-8"), "0.2.0\n")
            self.assertIn("## 0.2.0\n\n- Big one\n", (root / "CHANGELOG.md").read_text(encoding="utf-8"))

    def test_invalid_version_fails_without_writing(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "VERSION").write_text("latest\n", encoding="utf-8")
            code, _ = self.run_main(root, "--bump")
            self.assertEqual(code, 1)
            self.assertEqual((root / "VERSION").read_text(encoding="utf-8"), "latest\n")


if __name__ == "__main__":
    unittest.main()
