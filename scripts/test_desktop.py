"""Checks on the desktop app sources that CI can run without Electron."""

import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DESKTOP = ROOT / "desktop"


class DesktopAppTest(unittest.TestCase):
    def test_app_version_is_plain_semver_and_in_step(self):
        version = (DESKTOP / "VERSION").read_text(encoding="utf8").strip()
        self.assertRegex(version, r"^\d+\.\d+\.\d+$")
        pkg = json.loads((DESKTOP / "app" / "package.json").read_text(encoding="utf8"))
        self.assertEqual(pkg["version"], version)
        self.assertEqual(pkg["main"], "main.js")

    def test_electron_download_is_pinned(self):
        pin = json.loads((DESKTOP / "electron.json").read_text(encoding="utf8"))
        self.assertRegex(pin["version"], r"^\d+\.\d+\.\d+$")
        self.assertRegex(pin["sha256"]["win32-x64"], r"^[0-9a-f]{64}$")

    def test_pages_get_no_node_and_a_large_media_buffer(self):
        main = (DESKTOP / "app" / "main.js").read_text(encoding="utf8")
        self.assertIn("contextIsolation: true", main)
        self.assertIn("nodeIntegration: false", main)
        self.assertIn("sandbox: true", main)
        self.assertIn('"mse-video-buffer-size-limit-mb"', main)

    def test_local_pages_load_only_their_own_scripts(self):
        for page in ("setup.html", "offline.html", "updating.html"):
            html = (DESKTOP / "app" / page).read_text(encoding="utf8")
            self.assertIn("script-src 'self'", html)
            self.assertNotRegex(html, re.compile(r"<script>(?!</script>)", re.S))


if __name__ == "__main__":
    unittest.main()
