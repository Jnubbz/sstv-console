"use strict";

// The page talks to the engine on this machine. POSTs carry X-SSTV-Console, which the
// engine requires (a page from another site can't send it).

const $ = (id) => document.getElementById(id);

async function api(path, method = "GET") {
  const r = await fetch(path, { method, headers: method === "GET" ? {} : { "X-SSTV-Console": "1" } });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(body.error || r.statusText);
  return body;
}

let current = null;     // the version this page loaded with
let dismissed = "";     // "Later" on this version
let restarting = false;
let last = null;       // the last status shown

function mb(n) { return (n / 1048576).toFixed(1); }

function render(s) {
  const u = s.update;
  last = s;
  if (current === null) current = s.version;
  $("version").textContent = s.version === "dev" ? "(development build)" : s.version;

  // the menu's one-line summary
  let line = "";
  if (s.version === "dev") line = "Development build: it doesn't update itself.";
  else if (u.state === "checking") line = "Checking…";
  else if (u.error) line = u.error;
  else if (u.available) line = `Version ${u.latest} is ready.`;
  else if (u.checked) line = `Up to date. Checked ${new Date(u.checked).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}.`;
  $("update-line").textContent = line;
  $("check").disabled = u.state !== "";

  // the banner
  const busy = ["downloading", "installing", "restarting"].includes(u.state);
  const show = busy || (u.available && dismissed !== u.latest) || (u.error && u.error.startsWith("Update failed") && dismissed !== u.latest);
  $("update").hidden = !show;
  if (!show) return;
  const titles = {
    downloading: `Downloading SSTV Console ${u.latest}… ${u.total ? mb(u.done) + " of " + mb(u.total) + " MB" : ""}`,
    installing: "Installing…",
    restarting: "Restarting into the new version…",
  };
  $("update-title").textContent = titles[u.state] || (u.error ? u.error : `SSTV Console ${u.latest} is ready (you have ${s.version})`);
  $("update-notes").textContent = busy ? "" : (u.canInstall ? (u.notes || "") : "Download it from the Releases page on GitHub.");
  $("update-progress").hidden = u.state !== "downloading";
  $("update-bar").style.width = u.total ? `${Math.round((100 * u.done) / u.total)}%` : "0";
  $("update-install").hidden = busy || !u.canInstall;
  $("update-later").hidden = busy;
  if (u.state === "restarting") waitForNewVersion();
}

async function poll() {
  try { render(await api("/api/status")); } catch { /* engine restarting */ }
}

// After "restarting", the engine goes away and the new one comes back on the same address.
async function waitForNewVersion() {
  if (restarting) return;
  restarting = true;
  for (let i = 0; i < 120; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    try {
      const s = await api("/api/status");
      if (s.version !== current) { location.reload(); return; }
    } catch { /* not up yet */ }
  }
  $("update-title").textContent = "The new version didn't come back here. Open SSTV Console again.";
}

$("update-install").addEventListener("click", async () => {
  try { render(await api("/api/update/install", "POST")); }
  catch (e) { $("update-notes").textContent = e.message; }
});
$("update-later").addEventListener("click", () => { if (last) { dismissed = last.update.latest || ""; render(last); } });
$("check").addEventListener("click", async () => {
  $("update-line").textContent = "Checking…";
  try { const s = await api("/api/update/check", "POST"); dismissed = ""; render(s); }
  catch (e) { $("update-line").textContent = e.message; }
});
$("quit").addEventListener("click", async () => {
  try { await api("/api/quit", "POST"); } catch { /* gone */ }
  document.body.innerHTML = '<p class="quit-note">SSTV Console has quit. You can close this window.</p>';
});
$("menu-btn").addEventListener("click", () => {
  const open = $("menu").hidden;
  $("menu").hidden = !open;
  $("menu-btn").setAttribute("aria-expanded", String(open));
});
document.addEventListener("click", (e) => {
  if (!e.target.closest(".menu")) { $("menu").hidden = true; $("menu-btn").setAttribute("aria-expanded", "false"); }
});

poll();
setInterval(() => {
  // fast while something is happening, otherwise once a minute is plenty
  if (!$("update").hidden || !$("menu").hidden) poll();
}, 1500);
setInterval(poll, 60000);
