"use strict";

const params = new URLSearchParams(location.search);
const input = document.getElementById("server");
const button = document.getElementById("connect");
const error = document.getElementById("error");
input.value = params.get("server") || "";
input.focus();
input.select();

document.getElementById("form").addEventListener("submit", async (e) => {
  e.preventDefault();
  button.disabled = true;
  button.textContent = "Connecting...";
  error.textContent = "";
  const res = await window.viewdockDesktop.setServer(input.value);
  if (!res || !res.ok) {
    error.textContent = (res && res.error) || "Could not connect.";
    button.disabled = false;
    button.textContent = "Connect";
  }
});
