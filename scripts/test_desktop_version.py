import json
import subprocess
import tempfile
import unittest
from pathlib import Path

import desktop_version


def git(root: Path, *args: str) -> str:
    return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True).stdout.strip()


class DesktopVersionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        git(self.root, "init", "-q")
        git(self.root, "config", "user.email", "ci@example.com")
        git(self.root, "config", "user.name", "CI")
        (self.root / "desktop" / "app").mkdir(parents=True)
        (self.root / "desktop" / "VERSION").write_text("1.0.9\n", encoding="utf-8")
        (self.root / "desktop" / "app" / "package.json").write_text('{"name": "viewdock-desktop", "version": "1.0.9"}\n', encoding="utf-8")
        (self.root / "desktop" / "app" / "main.js").write_text("// app\n", encoding="utf-8")
        (self.root / "README").write_text("x\n", encoding="utf-8")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "start")
        self.start = git(self.root, "rev-parse", "HEAD")

    def tearDown(self):
        self.tmp.cleanup()

    def commit(self, path: str, text: str):
        (self.root / path).write_text(text, encoding="utf-8")
        git(self.root, "add", "-A")
        git(self.root, "commit", "-q", "-m", "change")

    def version(self) -> str:
        return (self.root / "desktop" / "VERSION").read_text(encoding="utf-8").strip()

    def package_version(self) -> str:
        return json.loads((self.root / "desktop" / "app" / "package.json").read_text(encoding="utf-8"))["version"]

    def test_a_change_to_the_app_raises_its_version(self):
        self.commit("desktop/app/main.js", "// changed\n")
        self.assertEqual(desktop_version.main(["--since", self.start, "--root", str(self.root)]), 0)
        self.assertEqual(self.version(), "1.1.0")
        self.assertEqual(self.package_version(), "1.1.0")

    def test_other_changes_keep_the_version(self):
        self.commit("README", "y\n")
        desktop_version.main(["--since", self.start, "--root", str(self.root)])
        self.assertEqual(self.version(), "1.0.9")

    def test_a_version_set_by_hand_is_kept(self):
        (self.root / "desktop" / "app" / "main.js").write_text("// changed\n", encoding="utf-8")
        self.commit("desktop/VERSION", "2.0.0\n")
        desktop_version.main(["--since", self.start, "--root", str(self.root)])
        self.assertEqual(self.version(), "2.0.0")
        self.assertEqual(self.package_version(), "2.0.0")


if __name__ == "__main__":
    unittest.main()
