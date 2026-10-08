"use strict";

const status = document.getElementById("status");
status.textContent = new URLSearchParams(location.search).get("status") || "Updating ViewDock...";

// The app reports each step (preparing, downloading) through this.
window.setStatus = (text) => {
  status.textContent = String(text);
};
