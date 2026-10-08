"use strict";

const params = new URLSearchParams(location.search);
document.getElementById("server").textContent = params.get("server") || "your server";
document.getElementById("detail").textContent = params.get("error") ? "Error: " + params.get("error") : "";
document.getElementById("retry").addEventListener("click", () => window.viewdockDesktop.retry());
document.getElementById("change").addEventListener("click", () => window.viewdockDesktop.changeServer());

// Try again by itself every 15 seconds, so a server restart needs no click.
setInterval(() => window.viewdockDesktop.retry(), 15_000);
