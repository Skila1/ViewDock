// The bridge the pages see. The ViewDock web app reads viewdockDesktop to
// size its media buffer for the app; the local setup and offline pages also
// get the actions that change the server. No Node.js reaches any page.
"use strict";

const { contextBridge, ipcRenderer } = require("electron");

const version = String(ipcRenderer.sendSync("viewdock:version") || "");

const bridge = {
  version,
  platform: process.platform,
  /** Megabytes of video the app's media buffer holds per stream. */
  mediaBufferMB: 1000,
};

if (location.protocol === "file:") {
  bridge.setServer = (url) => ipcRenderer.invoke("viewdock:set-server", String(url || ""));
  bridge.retry = () => ipcRenderer.invoke("viewdock:retry");
  bridge.changeServer = () => ipcRenderer.invoke("viewdock:change-server");
}

contextBridge.exposeInMainWorld("viewdockDesktop", Object.freeze(bridge));
