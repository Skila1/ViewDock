// ViewDock for Windows: the ViewDock web app in its own Chromium window,
// with what a browser tab cannot have: a media buffer large enough for 4K
// remuxes, no background throttling, and its own storage. The server it
// opens comes from server.json, which the ViewDock server that built this
// copy wrote next to the app; the viewer can change it.
"use strict";

const { app, BrowserWindow, Menu, dialog, ipcMain, net, session, shell } = require("electron");
const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

// Chromium keeps about 150 MB of video per stream for playback. A 4K remux
// runs 50 to 100 MB a segment, so a browser can hold one or two; the app
// holds a minute.
app.commandLine.appendSwitch("mse-video-buffer-size-limit-mb", "1000");
app.commandLine.appendSwitch("mse-audio-buffer-size-limit-mb", "100");
// A paused or hidden player keeps reporting and downloading on time.
app.commandLine.appendSwitch("disable-background-timer-throttling");
app.commandLine.appendSwitch("disable-renderer-backgrounding");
app.commandLine.appendSwitch("disable-backgrounding-occluded-windows");

app.setAppUserModelId("dev.viewdock.desktop");

const UPDATE_EVERY_MS = 6 * 60 * 60 * 1000;
const settingsFile = () => path.join(app.getPath("userData"), "settings.json");
const windowFile = () => path.join(app.getPath("userData"), "window.json");

function readJSON(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch {
    return null;
  }
}

function writeJSON(file, value) {
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, JSON.stringify(value, null, 2));
  } catch {
    /* not fatal: the app works without remembering */
  }
}

/** The server the installer was built for, written by that server. */
function bundled() {
  return readJSON(path.join(process.resourcesPath, "server.json")) || readJSON(path.join(__dirname, "server.json")) || {};
}

/** A server address the viewer may enter: http or https origins only. */
function normalizeServer(raw) {
  let text = String(raw || "").trim();
  if (!text) return "";
  if (!/^https?:\/\//i.test(text)) text = "https://" + text;
  let u;
  try {
    u = new URL(text);
  } catch {
    return "";
  }
  if ((u.protocol !== "https:" && u.protocol !== "http:") || !u.hostname || u.username || u.password) return "";
  return u.origin;
}

function serverURL() {
  const own = readJSON(settingsFile());
  return normalizeServer((own && own.server_url) || bundled().server_url || "");
}

function serverName() {
  return bundled().server_name || "ViewDock";
}

let win = null;
let pendingPath = "";

function sameOrigin(target, base) {
  try {
    return new URL(target).origin === new URL(base).origin;
  } catch {
    return false;
  }
}

// Discord sign-in runs inside the window; everything else leaves for the
// default browser.
function signInHost(target) {
  try {
    const host = new URL(target).hostname;
    return host === "discord.com" || host.endsWith(".discord.com") || host === "discordapp.com" || host.endsWith(".discordapp.com");
  } catch {
    return false;
  }
}

function openOutside(target) {
  try {
    const u = new URL(target);
    if (u.protocol === "https:" || u.protocol === "http:" || u.protocol === "mailto:") void shell.openExternal(u.href);
  } catch {
    /* ignore malformed links */
  }
}

function restoreBounds() {
  const saved = readJSON(windowFile());
  const bounds = { width: 1280, height: 800 };
  if (saved && Number.isFinite(saved.width) && Number.isFinite(saved.height)) {
    bounds.width = Math.max(800, saved.width);
    bounds.height = Math.max(500, saved.height);
    if (Number.isFinite(saved.x) && Number.isFinite(saved.y)) {
      bounds.x = saved.x;
      bounds.y = saved.y;
    }
  }
  return { bounds, maximized: Boolean(saved && saved.maximized) };
}

function saveBounds() {
  if (!win || win.isDestroyed() || win.isFullScreen()) return;
  writeJSON(windowFile(), { ...win.getNormalBounds(), maximized: win.isMaximized() });
}

function showLocal(page, query) {
  if (!win) return;
  void win.loadFile(path.join(__dirname, page), { query: { name: serverName(), ...(query || {}) } });
}

function openServer(subPath) {
  const base = serverURL();
  if (!base) {
    showLocal("setup.html", { server: bundled().server_url || "" });
    return;
  }
  let target = base + "/";
  if (subPath) {
    const u = new URL(subPath, base);
    if (u.origin === base) target = u.href;
  }
  void win.loadURL(target);
}

function createWindow() {
  const { bounds, maximized } = restoreBounds();
  win = new BrowserWindow({
    ...bounds,
    minWidth: 800,
    minHeight: 500,
    title: serverName(),
    backgroundColor: "#0b0b0f",
    autoHideMenuBar: true,
    show: false,
    icon: path.join(process.resourcesPath, "icon.ico"),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      backgroundThrottling: false,
      spellcheck: false,
    },
  });
  if (maximized) win.maximize();
  win.once("ready-to-show", () => win.show());
  win.on("resize", saveBounds);
  win.on("move", saveBounds);
  win.on("close", saveBounds);
  win.on("closed", () => {
    win = null;
  });

  const wc = win.webContents;
  wc.on("will-navigate", (event, target) => {
    const base = serverURL();
    if (target.startsWith("file:")) return;
    if (base && (sameOrigin(target, base) || signInHost(target))) return;
    event.preventDefault();
    openOutside(target);
  });
  wc.on("will-redirect", (event, target) => {
    const base = serverURL();
    if (base && (sameOrigin(target, base) || signInHost(target))) return;
    event.preventDefault();
    openOutside(target);
  });
  wc.setWindowOpenHandler(({ url }) => {
    const base = serverURL();
    if (base && (sameOrigin(url, base) || signInHost(url))) {
      return {
        action: "allow",
        overrideBrowserWindowOptions: {
          autoHideMenuBar: true,
          backgroundColor: "#0b0b0f",
          webPreferences: { preload: path.join(__dirname, "preload.js"), contextIsolation: true, nodeIntegration: false, sandbox: true },
        },
      };
    }
    openOutside(url);
    return { action: "deny" };
  });
  wc.on("did-fail-load", (_event, code, description, url, isMainFrame) => {
    // -3 is an aborted load (a navigation replaced it), not a failure.
    if (!isMainFrame || code === -3 || url.startsWith("file:")) return;
    showLocal("offline.html", { error: description || String(code), server: serverURL() });
  });
  wc.on("render-process-gone", (_event, details) => {
    if (details.reason !== "clean-exit") showLocal("offline.html", { error: "The page stopped (" + details.reason + ").", server: serverURL() });
  });
  wc.on("before-input-event", (event, input) => {
    if (input.type !== "keyDown") return;
    if (input.key === "F11") {
      win.setFullScreen(!win.isFullScreen());
      event.preventDefault();
    } else if (input.key === "F5" || (input.control && input.key.toLowerCase() === "r")) {
      wc.reload();
      event.preventDefault();
    } else if (input.control && input.shift && input.key.toLowerCase() === "i") {
      wc.toggleDevTools();
      event.preventDefault();
    } else if (input.alt && input.key === "ArrowLeft" && wc.navigationHistory.canGoBack()) {
      wc.navigationHistory.goBack();
      event.preventDefault();
    } else if (input.alt && input.key === "ArrowRight" && wc.navigationHistory.canGoForward()) {
      wc.navigationHistory.goForward();
      event.preventDefault();
    }
  });
}

function buildMenu() {
  const template = [
    {
      label: "File",
      submenu: [
        { label: "Home", accelerator: "Alt+Home", click: () => openServer("") },
        { label: "Change server...", click: () => showLocal("setup.html", { server: serverURL() }) },
        { type: "separator" },
        { role: "quit" },
      ],
    },
    {
      label: "View",
      submenu: [
        { role: "reload" },
        { role: "togglefullscreen" },
        { type: "separator" },
        { role: "resetZoom" },
        { role: "zoomIn" },
        { role: "zoomOut" },
        { type: "separator" },
        { role: "toggleDevTools" },
      ],
    },
    {
      label: "Help",
      submenu: [
        { label: "Check for updates", click: () => void checkForUpdates() },
        {
          label: "About ViewDock",
          click: () =>
            void dialog.showMessageBox(win, {
              type: "info",
              title: "About ViewDock",
              message: "ViewDock " + app.getVersion(),
              detail: "Server: " + (serverURL() || "not set") + "\nElectron " + process.versions.electron + ", Chromium " + process.versions.chrome,
            }),
        },
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

/** Compares dotted versions: 1 when a is newer than b. */
function newer(a, b) {
  const pa = String(a).split(".").map((n) => parseInt(n, 10) || 0);
  const pb = String(b).split(".").map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) > (pb[i] || 0);
  }
  return false;
}

let updating = false;
// An installer downloaded while the app was open, installed when it quits.
let pendingInstaller = "";

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** What the server offers when it has a newer app than this one, or null. */
async function newerApp(timeoutMs) {
  const base = serverURL();
  if (!base || process.platform !== "win32") return null;
  const res = await session.defaultSession.fetch(base + "/api/v1/desktop", { cache: "no-store", signal: AbortSignal.timeout(timeoutMs) });
  if (!res.ok) throw new Error("the server answered " + res.status);
  const info = await res.json();
  if (!info || !info.available || !info.version || !info.windows || !newer(info.version, app.getVersion())) return null;
  return info;
}

/**
 * Downloads the installer the server built for itself, with the viewer's
 * session. The server answers 202 while it is still packaging the new
 * version; this waits up to waitMs for it.
 */
async function downloadInstaller(info, waitMs, onStatus) {
  const base = serverURL();
  const until = Date.now() + waitMs;
  for (;;) {
    const res = await session.defaultSession.fetch(base + info.windows.url, { cache: "no-store" });
    if (res.status === 202) {
      if (Date.now() > until) throw new Error("the server is still preparing the update");
      if (onStatus) onStatus("Your server is preparing ViewDock " + info.version + "...");
      await sleep(5000);
      continue;
    }
    if (!res.ok) throw new Error("the download failed (" + res.status + ")");
    if (onStatus) onStatus("Downloading ViewDock " + info.version + "...");
    const file = path.join(app.getPath("temp"), "ViewDock-Setup-" + info.version + ".exe");
    fs.writeFileSync(file, Buffer.from(await res.arrayBuffer()));
    return file;
  }
}

/** Runs an installer silently; with relaunch it opens the app again. */
function runInstaller(file, relaunch) {
  const args = relaunch ? ["/S", "/RELAUNCH"] : ["/S"];
  spawn(file, args, { detached: true, stdio: "ignore" }).unref();
}

/**
 * Every launch asks the server for a newer app first and, when there is
 * one, installs it and reopens before showing anything else. A server that
 * does not answer quickly, or a viewer who is not signed in yet, only
 * delays the update to the next launch.
 */
async function updateOnLaunch() {
  let info;
  try {
    info = await newerApp(6000);
  } catch {
    return false;
  }
  if (!info) return false;
  updating = true;
  showLocal("updating.html", { status: "Updating ViewDock to " + info.version + "..." });
  try {
    const file = await downloadInstaller(info, 3 * 60 * 1000, (status) => {
      if (win && !win.isDestroyed()) void win.webContents.executeJavaScript("window.setStatus && window.setStatus(" + JSON.stringify(status) + ")").catch(() => {});
    });
    runInstaller(file, true);
    app.quit();
    return true;
  } catch {
    updating = false;
    return false;
  }
}

// While the app stays open for hours, a newer version is downloaded in the
// background and installed when the app is closed.
async function updateInBackground() {
  if (updating || pendingInstaller) return;
  try {
    const info = await newerApp(15000);
    if (!info) return;
    pendingInstaller = await downloadInstaller(info, 10 * 60 * 1000);
  } catch {
    /* tried again at the next check */
  }
}

async function checkForUpdates() {
  if (updating) return;
  try {
    const info = await newerApp(15000);
    if (!info) {
      void dialog.showMessageBox(win, { type: "info", message: "ViewDock is up to date.", detail: "Version " + app.getVersion() });
      return;
    }
    updating = true;
    const file = pendingInstaller || (await downloadInstaller(info, 5 * 60 * 1000));
    runInstaller(file, true);
    app.quit();
  } catch (err) {
    updating = false;
    void dialog.showMessageBox(win, { type: "error", message: "Could not update ViewDock.", detail: String(err && err.message ? err.message : err) });
  }
}

ipcMain.on("viewdock:version", (event) => {
  event.returnValue = app.getVersion();
});

ipcMain.handle("viewdock:set-server", async (event, raw) => {
  if (!event.senderFrame || !event.senderFrame.url.startsWith("file:")) return { ok: false, error: "Not allowed." };
  const base = normalizeServer(raw);
  if (!base) return { ok: false, error: "Enter an address such as https://viewdock.example.com." };
  try {
    const res = await net.fetch(base + "/healthz", { cache: "no-store" });
    if (!res.ok) return { ok: false, error: "That server answered " + res.status + "." };
  } catch (err) {
    return { ok: false, error: "Could not reach that server: " + (err && err.message ? err.message : err) };
  }
  writeJSON(settingsFile(), { ...(readJSON(settingsFile()) || {}), server_url: base });
  win.setTitle(serverName());
  openServer("");
  return { ok: true };
});

ipcMain.handle("viewdock:retry", (event) => {
  if (!event.senderFrame || !event.senderFrame.url.startsWith("file:")) return;
  openServer("");
});

ipcMain.handle("viewdock:change-server", (event) => {
  if (!event.senderFrame || !event.senderFrame.url.startsWith("file:")) return;
  showLocal("setup.html", { server: serverURL() });
});

// viewdock://watch/movie/<id> opens that page of the server.
function deepLinkPath(argv) {
  const link = argv.find((a) => typeof a === "string" && a.toLowerCase().startsWith("viewdock://"));
  if (!link) return "";
  try {
    const u = new URL(link);
    return "/" + [u.hostname, u.pathname.replace(/^\/+/, "")].filter(Boolean).join("/") + u.search;
  } catch {
    return "";
  }
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", (_event, argv) => {
    if (!win) return;
    if (win.isMinimized()) win.restore();
    win.focus();
    const link = deepLinkPath(argv);
    if (link) openServer(link);
  });
  app.setAsDefaultProtocolClient("viewdock");
  pendingPath = deepLinkPath(process.argv);

  app.whenReady().then(() => {
    // Only what the web app uses: fullscreen video, notifications, and
    // copying links.
    session.defaultSession.setPermissionRequestHandler((wc, permission, callback, details) => {
      const base = serverURL();
      const ok = ["fullscreen", "notifications", "clipboard-sanitized-write", "pointerLock"].includes(permission);
      callback(ok && Boolean(base) && sameOrigin(details.requestingUrl || wc.getURL(), base));
    });
    session.defaultSession.setUserAgent(session.defaultSession.getUserAgent() + " ViewDockDesktop/" + app.getVersion());
    buildMenu();
    createWindow();
    void updateOnLaunch().then((updatingNow) => {
      if (updatingNow) return;
      openServer(pendingPath);
      pendingPath = "";
      setInterval(() => void updateInBackground(), UPDATE_EVERY_MS);
    });
  });

  app.on("window-all-closed", () => app.quit());
  app.on("will-quit", () => {
    if (pendingInstaller && !updating) runInstaller(pendingInstaller, false);
  });
}
