# Windows app

ViewDock for Windows is the ViewDock web app in its own window. It plays the same libraries with the same account, and adds what a browser tab cannot have:

- a media buffer about seven times larger than a browser's, so 4K originals stream a minute ahead instead of a few seconds;
- no background throttling, so a paused or hidden player keeps its session and buffer;
- hardware decoding of HEVC (including 10-bit HDR) wherever Windows has a decoder for it;
- its own storage, unaffected by browser data clearing.

The app needs Windows 10 or 11 (64-bit). Inside the Discord Activity, playback still runs in Discord's built-in browser.

## Turning it on

The app is off until an administrator turns on **ViewDock for Windows** in Admin, Settings (Desktop app). ViewDock then:

1. downloads the pinned release of Electron for Windows (about 150 MB) from the Electron project and checks it against the SHA-256 pinned in ViewDock;
2. builds the app from it: the program is renamed `ViewDock.exe` and given ViewDock's name, version and icon, and ViewDock's own app files are added;
3. packages it for this server when someone asks for it.

Everything lives in the cache folder, `./cache/desktop` beside `docker-compose.yml`, and takes about 700 MB. Turning the setting off deletes it. The Docker image does not contain the app; it carries only its small source files and NSIS, which builds the installer.

`VD_DESKTOP_ELECTRON_MIRROR` downloads Electron from a mirror with the same layout as the Electron releases on GitHub instead.

## Getting the app

In a Windows browser, signed-in users see **Download for Windows** at the bottom of the sidebar, above their profile, and **Get the app** in the top bar. Both lead to **Profile, ViewDock for Windows**, which is also linked from the account menu. Two downloads are offered:

- **Download for Windows**: an installer for the current Windows account. It needs no administrator rights, installs to `%LOCALAPPDATA%\Programs\ViewDock`, and adds Start menu and desktop shortcuts and an entry in **Settings, Apps** for removal.
- **Portable version**: a zip that runs from any folder without installing.

The first download after the app is turned on or updated waits a few minutes while the server builds it; the download button says so.

The app is not code signed, so Windows SmartScreen may say it is unrecognised. Choose **More info**, then **Run anyway**.

## Built by each server

Every package contains one setting file with the server's public address and nothing else: no account, token or secret. An installer downloaded from one server opens that server.

The address is the **Public URL** from Admin, Settings (or `VD_PUBLIC_URL`). Without one, it is the address the user downloaded from, so set the public URL when users reach ViewDock through more than one address. Without NSIS (`makensis`), as when ViewDock runs outside Docker, only the portable version is offered.

## Updates

The app has its own version, separate from ViewDock's. Each ViewDock release that changes the app raises it.

Every time the app starts, it asks its server for a newer version. When there is one, it downloads the installer the server built, installs it without asking and reopens, before showing anything else. A server that does not answer within a few seconds, or a user who is not signed in, only delays the update to the next start. While the app stays open, it checks every six hours and installs a newer version when it is closed. **Help, Check for updates** installs one at once.

## Using the app

| Action | How |
|--------|-----|
| Full screen | F11, or the player's full screen button |
| Reload | F5 or Ctrl+R |
| Back and forward | Alt+Left and Alt+Right |
| Menu (change server, updates, about) | Press Alt |

**Change server** in the File menu connects the app to a different ViewDock server; the choice is kept for the Windows account. Links outside ViewDock open in the default browser, while Discord sign-in stays in the app.

`viewdock://` links open the app at a page of its server, for example `viewdock://watch/movie/<id>`.

## Removing the app

Remove **ViewDock** in **Settings, Apps**. Sign-in data and settings stay in `%APPDATA%\ViewDock` until that folder is deleted.

## Development

The app lives in `desktop/`: `desktop/app` is the Electron app (`main.js`, `preload.js` and the local setup, offline and update pages), `desktop/electron.json` pins the Electron version and the SHA-256 of its Windows download, and `desktop/VERSION` is the app's version. They are embedded in the ViewDock binary; `internal/desktop` downloads Electron, builds the app and packages it.

The docker workflow raises `desktop/VERSION` (and the version in `desktop/app/package.json`) on every `DEPLOY:` push that changes `desktop/`, with `scripts/desktop_version.py`, unless the push set it by hand. To move to a newer Electron, update the version and SHA-256 in `desktop/electron.json` from the release's `SHASUMS256.txt`.
