"use strict";

// The page talks to the engine on this machine. POSTs carry X-SSTV-Console, which the
// engine requires (a page from another site can't send it).

const $ = (id) => document.getElementById(id);

async function api(path, method = "GET", body) {
  const opt = { method, headers: method === "GET" ? {} : { "X-SSTV-Console": "1" } };
  if (body instanceof ArrayBuffer) opt.body = body;
  else if (body !== undefined) opt.body = JSON.stringify(body);
  const r = await fetch(path, opt);
  if (r.status === 204) return {};
  const reply = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(reply.error || r.statusText);
  return reply;
}

let current = null;     // the version this page loaded with
let dismissed = "";     // "Later" on this version
let restarting = false;
let last = null;       // the last status shown

function mb(n) { return (n / 1048576).toFixed(1); }

function render(s) {
  const u = s.update;
  last = s;
  renderRadio(s);
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

// ---- the radio: sound in, CAT, live picture, received pictures ----

let audio = null;        // { ctx, stream, analyser }
let audioWanted = false; // tried the saved input once
let liveV = -1, picsV = -1, selected = "";
let portsLoaded = false;

function mhz(hz) { // 14230000 -> "14.230.00"
  const p = (n, w) => String(n).padStart(w, "0");
  return `${Math.floor(hz / 1e6)}.${p(Math.floor(hz % 1e6 / 1000), 3)}.${p(Math.floor(hz % 1000 / 10), 2)}`;
}

function renderRadio(s) {
  const rig = s.rig, rx = s.rx;
  // top bar
  $("rig-dot").className = "dot " + (rig.connected && !rig.error ? "dot-on" : "dot-off");
  $("rig-freq").hidden = !rig.freqHz;
  if (rig.freqHz) $("rig-freq").textContent = mhz(rig.freqHz);
  $("rig-text").textContent = rig.connected ? (rig.error || [rig.model, rig.mode].filter(Boolean).join(" · ")) : "No radio connected";
  // band buttons: one per band, lit when the radio is on that band's SSTV frequency
  const bands = $("bands");
  bands.hidden = !rig.connected;
  if (!bands.children.length) {
    for (const b of s.bands) {
      const el = document.createElement("button");
      el.className = "band"; el.textContent = b.name; el.dataset.hz = b.freqHz;
      el.title = `${mhz(b.freqHz)} ${b.lower ? "LSB" : "USB"}`;
      el.addEventListener("click", async () => {
        $("band-msg").textContent = "";
        try { render(await api("/api/rig/tune", "POST", { band: b.name })); }
        catch (e) { $("band-msg").textContent = e.message; }
      });
      bands.append(el);
    }
  }
  for (const el of bands.children) {
    const on = Number(el.dataset.hz) === rig.freqHz;
    el.classList.toggle("on", on); el.setAttribute("aria-pressed", String(on));
  }
  if (!rig.connected) $("band-msg").textContent = "";
  const state = s.tx.keyed ? ["pill-tx", "dot-tx", "Transmitting " + s.tx.mode] : !rx.audio ? ["pill-idle", "dot-off", "No audio"] : rx.receiving ? ["pill-busy", "dot-on", "Receiving " + rx.mode] : ["pill-rx", "dot-on", "Listening"];
  $("rx-pill").className = "pill " + state[0];
  $("rx-dot").className = "dot " + state[1];
  $("rx-text").textContent = state[2];

  // radio panel
  const pct = rx.audio ? Math.max(0, Math.min(100, (rx.levelDb + 60) * 100 / 60)) : 0;
  $("level").style.width = pct + "%";
  $("level").className = rx.levelDb > -1 ? "hot" : "";
  if (audio && rx.audio) {
    $("audio-msg").className = "small " + (rx.levelDb > -1 ? "err" : "muted");
    $("audio-msg").textContent = rx.levelDb > -1 ? "Too loud: it's clipping. Turn the radio's USB output level down."
      : rx.levelDb < -50 ? "Audio is on, but very quiet. Check the radio's USB output level." : "Audio is on.";
  }
  $("rec-btn").disabled = !rx.audio || rx.recording > 0;
  if (rx.recording > 0) $("rec-msg").textContent = `Recording… ${rx.recording} s left`;
  else if (rx.recorded) $("rec-msg").textContent = "Saved: " + rx.recorded;
  renderStation(s);
  $("rig-btn").textContent = rig.connected ? "Disconnect" : "Connect";
  if (rig.connected) { $("rig-msg").className = "small " + (rig.error ? "err" : "muted"); $("rig-msg").textContent = rig.error || `Connected to the ${rig.model}.`; }

  // start the saved input once (the Mac asks for the microphone the first time)
  if (!audioWanted) {
    audioWanted = true;
    if (s.settings.audioDevice) startAudio(s.settings.audioDevice, true);
    if (!portsLoaded) loadPorts(s);
  }

  // live picture
  if (rx.liveV !== liveV) {
    liveV = rx.liveV;
    if (liveV > 0) {
      $("live-img").src = "/api/live.png?v=" + liveV;
      $("live-img").hidden = false; $("live-wait").hidden = true; $("live-box").classList.add("has");
    }
  }
  const got = rx.receiving || liveV > 0;
  $("st-mode").textContent = got && rx.mode ? rx.mode : "—";
  $("st-line").textContent = rx.receiving ? (rx.joined ? `Row ${rx.line}` : `${Math.round(100 * rx.line / rx.lines)}%`) : (liveV > 0 ? "Done" : "—");
  $("st-off").textContent = got ? `${rx.offsetHz >= 0 ? "+" : ""}${Math.round(rx.offsetHz)} Hz` : "—";
  $("st-slant").textContent = got ? (Math.abs(rx.slantPpm) < 30 ? "None" : "Fixed") : "—";
  $("live-bar").style.width = rx.receiving ? `${100 * rx.line / rx.lines}%` : "0";
  $("live-note").textContent = rx.note || "";
  $("rx-reset").disabled = !rx.receiving;

  if (rx.picturesV !== picsV) { picsV = rx.picturesV; loadPictures(true); }
  if (!selected && $("composer").hidden) {
    $("hello-title").textContent = rx.audio ? "Listening" : "Connect your radio";
    $("hello-text").textContent = rx.audio ? "Pictures decode on their own when a station sends one. Tune to an SSTV frequency and wait for one to start."
      : "Open Radio and turn on audio. Pictures decode on their own when a station sends one.";
    $("hello-btn").hidden = rx.audio;
  }
}

async function loadPorts(s) {
  portsLoaded = true;
  try {
    const p = await api("/api/ports");
    const sel = $("rig-port");
    sel.replaceChildren();
    if (!p.ports.length) sel.add(new Option("No serial ports found", ""));
    for (const x of p.ports) sel.add(new Option(x.label, x.path));
    if (s.settings.rigPort) sel.value = s.settings.rigPort;
    const b = $("rig-baud");
    b.replaceChildren();
    for (const x of p.bauds) b.add(new Option(String(x), String(x)));
    b.value = String(s.rig.baud || s.settings.rigBaud || 38400);
  } catch { portsLoaded = false; }
}

async function listInputs(keep) {
  const all = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === "audioinput" && d.deviceId !== "default" && d.deviceId !== "communications");
  const sel = $("audio-dev");
  sel.replaceChildren();
  for (const d of all) sel.add(new Option(d.label || "Sound input", d.deviceId));
  if (keep && all.some((d) => d.deviceId === keep)) sel.value = keep;
  else { const usb = all.find((d) => /usb audio|codec/i.test(d.label)); if (usb) sel.value = usb.deviceId; }
  return all;
}

function stopAudio() {
  if (!audio) return;
  audio.stream.getTracks().forEach((t) => t.stop());
  audio.ctx.close();
  audio = null;
}

// startAudio opens the input and streams it to the engine. quiet: the saved input at
// startup (don't shout if it's unplugged).
async function startAudio(deviceId, quiet) {
  const msg = $("audio-msg");
  const want = { echoCancellation: false, noiseSuppression: false, autoGainControl: false, channelCount: 1 };
  try {
    stopAudio();
    if (!navigator.mediaDevices) throw new Error("this window can't use sound inputs");
    if (!deviceId) {
      // first time: ask for permission with any input, then the list has names to pick from
      (await navigator.mediaDevices.getUserMedia({ audio: want })).getTracks().forEach((t) => t.stop());
      await listInputs("");
      deviceId = $("audio-dev").value;
    }
    const stream = await navigator.mediaDevices.getUserMedia({ audio: deviceId ? { ...want, deviceId: { exact: deviceId } } : want });
    const ctx = new AudioContext();
    await ctx.audioWorklet.addModule("worklet.js");
    const src = ctx.createMediaStreamSource(stream);
    const tap = new AudioWorkletNode(ctx, "sstv-tap");
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 4096; analyser.smoothingTimeConstant = 0;
    const mute = ctx.createGain(); mute.gain.value = 0; // keeps the graph running, plays nothing
    src.connect(analyser); src.connect(tap); tap.connect(mute); mute.connect(ctx.destination);
    let parts = [], have = 0, sending = Promise.resolve(), backlog = 0;
    tap.port.onmessage = (e) => {
      parts.push(e.data); have += e.data.length;
      if (have < 4096) return;
      const out = new Float32Array(have); let o = 0;
      for (const p of parts) { out.set(p, o); o += p.length; }
      parts = []; have = 0;
      if (backlog > 40) return; // the engine is gone or far behind: drop rather than pile up
      backlog++;
      sending = sending.then(() => api("/api/audio?rate=" + ctx.sampleRate, "POST", out.buffer)).catch(() => {}).finally(() => { backlog--; });
    };
    audio = { ctx, stream, analyser };
    if (ctx.state === "suspended") await ctx.resume().catch(() => {});
    const used = stream.getAudioTracks()[0];
    const id = deviceId || used.getSettings().deviceId || "";
    await listInputs(id);
    listOutputs();
    $("audio-btn").textContent = "Turn off audio";
    msg.className = "small muted"; msg.textContent = "Audio is on.";
    api("/api/settings/audio", "POST", { ID: id, Label: used.label || "" }).catch(() => {});
    used.addEventListener("ended", () => { stopAudio(); $("audio-btn").textContent = "Turn on audio"; msg.className = "small err"; msg.textContent = "The sound input went away (unplugged?)."; });
  } catch (e) {
    stopAudio();
    $("audio-btn").textContent = "Turn on audio";
    msg.className = "small err";
    msg.textContent = e.name === "NotAllowedError" ? "The microphone permission was refused. Allow SSTV Console to use the microphone in your system's privacy settings, then try again."
      : quiet ? "The sound input used last time isn't there. Pick one and turn on audio."
      : "Couldn't open that input: " + e.message;
    if (quiet) $("radio").hidden = false;
  }
}

$("audio-btn").addEventListener("click", () => {
  if (audio) {
    stopAudio(); $("audio-btn").textContent = "Turn on audio"; $("audio-msg").textContent = "";
    api("/api/settings/audio", "POST", { ID: "", Label: "" }).catch(() => {});
  } else startAudio($("audio-dev").value, false);
});
$("audio-dev").addEventListener("change", () => { if ($("audio-dev").value) startAudio($("audio-dev").value, false); });

$("rig-btn").addEventListener("click", async () => {
  const msg = $("rig-msg");
  $("rig-btn").disabled = true;
  try {
    if (!(last && last.rig.connected) && !$("rig-port").value) throw new Error("No serial port found. Plug in the radio's USB cable, then close and reopen this panel.");
    if (last && last.rig.connected) { render(await api("/api/rig/disconnect", "POST")); msg.textContent = ""; }
    else { msg.className = "small muted"; msg.textContent = "Connecting…"; render(await api("/api/rig/connect", "POST", { port: $("rig-port").value, baud: Number($("rig-baud").value) })); }
  } catch (e) { msg.className = "small err"; msg.textContent = e.message; }
  $("rig-btn").disabled = false;
});

function toggleRadio(open) {
  const show = open === undefined ? $("radio").hidden : open;
  $("radio").hidden = !show;
  $("radio-btn").setAttribute("aria-expanded", String(show));
  if (show && last) loadPorts(last);
}
$("radio-btn").addEventListener("click", () => toggleRadio());
$("rig-pill").addEventListener("click", () => toggleRadio(true));
$("hello-btn").addEventListener("click", () => toggleRadio(true));
$("rec-btn").addEventListener("click", () => api("/api/rx/record", "POST").then(render).catch(() => {}));
$("rx-reset").addEventListener("click", () => api("/api/rx/reset", "POST").then(render).catch(() => {}));

function when(iso) {
  const d = new Date(iso), today = new Date().toDateString() === d.toDateString();
  return (today ? "" : d.toLocaleDateString([], { month: "short", day: "numeric" }) + " ") + d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function show(p) {
  selected = p.id; shown = p;
  $("viewer-empty").hidden = true; $("viewer").hidden = false; $("composer").hidden = true;
  $("viewer-call").value = p.call || ""; $("reply-btn").hidden = !!p.sent;
  $("viewer-img").src = "/api/picture/" + encodeURIComponent(p.id) + ".png";
  $("viewer-img").alt = "Received picture, " + p.mode;
  $("viewer-cap").textContent = [when(p.time), p.mode, p.freqHz ? mhz(p.freqHz) + " MHz" : "", `${p.w}×${p.h}`].filter(Boolean).join(" · ");
  for (const b of document.querySelectorAll(".pic")) b.classList.toggle("on", b.dataset.id === p.id);
}

async function loadPictures(showNewest) {
  let list;
  try { list = await api("/api/pictures"); } catch { return; }
  $("inbox-empty").hidden = list.length > 0;
  $("pic-count").textContent = list.length ? String(list.length) : "";
  const ul = $("pics");
  ul.replaceChildren();
  for (const p of list) {
    const li = document.createElement("li"), b = document.createElement("button"), img = document.createElement("img"), t = document.createElement("span");
    b.className = "pic" + (p.id === selected ? " on" : ""); b.dataset.id = p.id;
    img.src = "/api/picture/" + encodeURIComponent(p.id) + ".png"; img.alt = ""; img.loading = "lazy";
    t.className = "pic-text";
    const a = document.createElement("strong"), c = document.createElement("span");
    a.textContent = p.call || when(p.time);
    if (p.sent) { const tag = document.createElement("span"); tag.className = "tag"; tag.textContent = "Sent"; a.append(tag); }
    c.className = "muted small"; c.textContent = [p.call ? when(p.time) : "", p.mode, p.freqHz ? mhz(p.freqHz) : ""].filter(Boolean).join(" · ");
    t.append(a, c); b.append(img, t); b.addEventListener("click", () => show(p)); li.append(b); ul.append(li);
  }
  if (showNewest && list.length && picsV > 0 && $("composer").hidden) show(list[0]); // a picture just arrived
}

// ---- sending ----

let shown = null;       // the picture in the viewer
let photo = null;       // my photo, behind the text
let tpl = "cq";
let stationSet = false; // the station fields were filled from the settings
let player = null;      // the <audio> playing a transmission
let theirs = null;      // replying: the picture I received from them
const colors = [["#FFFFFF", "White"], ["#FFE45C", "Yellow"], ["#FF9F1C", "Orange"], ["#FF4D4D", "Red"], ["#FF5CD6", "Pink"], ["#5CFF7A", "Green"], ["#5CE1FF", "Cyan"], ["#6E9BFF", "Blue"]];
const look = { top: "#FFFFFF", bottom: "#FFE45C", bar: true };

function swatches(id, key, save) {
  for (const [hex, name] of colors) {
    const b = document.createElement("button");
    b.className = "sw sw-" + hex.slice(1); b.title = name; b.setAttribute("aria-label", name); b.dataset.hex = hex;
    b.addEventListener("click", () => { look[key] = hex; markSwatches(); draw(); saveStation({ [save]: hex }); });
    $(id).append(b);
  }
}
function markSwatches() {
  for (const [id, key] of [["sw-top", "top"], ["sw-bottom", "bottom"]])
    for (const b of $(id).children) { const on = b.dataset.hex === look[key]; b.classList.toggle("on", on); b.setAttribute("aria-pressed", String(on)); }
}
const canSink = "setSinkId" in HTMLMediaElement.prototype;

function renderStation(s) {
  if (!stationSet) {
    stationSet = true;
    $("my-call").value = s.settings.myCall; $("my-grid").value = s.settings.myGrid;
    $("tx-level").value = s.settings.txLevel; $("tx-level-n").textContent = s.settings.txLevel + "%";
    const m = $("tx-mode");
    for (const x of s.modes) m.add(new Option(`${x.name} (${Math.floor(x.seconds / 60)}:${String(Math.round(x.seconds % 60)).padStart(2, "0")})`, x.name));
    m.value = s.settings.txMode;
    look.top = s.settings.topColor; look.bottom = s.settings.bottomColor; look.bar = !s.settings.noTextBar;
    swatches("sw-top", "top", "TopColor"); swatches("sw-bottom", "bottom", "BottomColor"); markSwatches();
    $("tx-textbar").checked = look.bar;
    $("tx-textbar").addEventListener("change", () => { look.bar = $("tx-textbar").checked; draw(); saveStation({ NoTextBar: !look.bar }); });
    if (!canSink) $("out-msg").textContent = "This system can't choose an output from inside the app: transmit audio plays on the computer's current sound output. Set that to the radio before transmitting.";
    listOutputs(s.settings.outDevice);
    const img = new Image();
    img.onload = () => { photo = img; draw(); };
    img.src = "/api/tx/photo";
    setTemplate("cq");
  }
  const t = s.tx;
  $("tx-go").disabled = t.keyed; $("tx-stop").disabled = !t.keyed;
  if (t.keyed) {
    $("tx-bar").style.width = Math.min(100, 100 * t.elapsed / t.seconds) + "%";
    $("tx-msg").className = "small muted";
    $("tx-msg").textContent = `Transmitting ${t.mode}: ${Math.max(0, Math.round(t.seconds - t.elapsed))} s left` + (t.ptt ? "" : " (no radio connected: audio only, nothing is keyed)");
  } else if (!player) $("tx-bar").style.width = "0";
  if (t.error) { $("tx-msg").className = "small err"; $("tx-msg").textContent = t.error; }
}

async function listOutputs(keep) {
  if (!navigator.mediaDevices) return;
  const outs = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === "audiooutput" && d.deviceId !== "default" && d.deviceId !== "communications" && d.label);
  const sel = $("out-dev");
  const before = keep !== undefined ? keep : sel.value;
  sel.replaceChildren();
  if (!canSink) { sel.add(new Option("The computer's current output", "")); sel.disabled = true; return; }
  sel.add(new Option(outs.length ? "Pick the radio's output…" : "Turn on audio to list outputs", ""));
  for (const d of outs) sel.add(new Option(d.label, d.deviceId));
  if (before && outs.some((d) => d.deviceId === before)) sel.value = before;
  else if (before && last && last.settings.outLabel) { sel.add(new Option(last.settings.outLabel, before)); sel.value = before; }
  $("out-pick").hidden = outs.length > 0 || !navigator.mediaDevices.selectAudioOutput;
}

function saveStation(more) {
  return api("/api/settings/station", "POST", { MyCall: $("my-call").value, MyGrid: $("my-grid").value, ...more }).then((s) => { last = s; }).catch(() => {});
}
for (const id of ["my-call", "my-grid"]) $(id).addEventListener("change", () => saveStation().then(() => { setTemplate(tpl); }));
$("out-dev").addEventListener("change", () => { const o = $("out-dev").selectedOptions[0]; saveStation({ OutDevice: o.value, OutLabel: o.value ? o.textContent : "" }); });
$("out-pick").addEventListener("click", async () => {
  try {
    const d = await navigator.mediaDevices.selectAudioOutput();
    await saveStation({ OutDevice: d.deviceId, OutLabel: d.label });
    listOutputs(d.deviceId);
  } catch { /* cancelled */ }
});
$("tx-level").addEventListener("input", () => { $("tx-level-n").textContent = $("tx-level").value + "%"; if (player) player.volume = $("tx-level").value / 100; });
$("tx-level").addEventListener("change", () => saveStation({ TxLevel: Number($("tx-level").value) }));

// the output for a transmission, or an error saying what to do
async function route(el) {
  el.volume = $("tx-level").value / 100;
  if (!canSink) return;
  const id = $("out-dev").value;
  if (!id) { toggleRadio(true); throw new Error("Pick the output that feeds the radio first (Radio, step 3), so the picture doesn't play through your speakers."); }
  await el.setSinkId(id);
}

$("tone-btn").addEventListener("click", async () => {
  const rate = 48000, n = rate * 1.5, b = new DataView(new ArrayBuffer(44 + n * 2));
  const str = (o, t) => [...t].forEach((c, i) => b.setUint8(o + i, c.charCodeAt(0)));
  str(0, "RIFF"); b.setUint32(4, 36 + n * 2, true); str(8, "WAVEfmt "); b.setUint32(16, 16, true); b.setUint16(20, 1, true); b.setUint16(22, 1, true);
  b.setUint32(24, rate, true); b.setUint32(28, rate * 2, true); b.setUint16(32, 2, true); b.setUint16(34, 16, true); str(36, "data"); b.setUint32(40, n * 2, true);
  for (let i = 0; i < n; i++) b.setInt16(44 + i * 2, 0.8 * 32767 * Math.sin(2 * Math.PI * 1500 * i / rate), true);
  const el = new Audio(URL.createObjectURL(new Blob([b], { type: "audio/wav" })));
  try { await route(el); await el.play(); $("out-msg").className = "small muted"; $("out-msg").textContent = "Playing a 1500 Hz tone on the chosen output. The radio is not keyed."; }
  catch (e) { $("out-msg").className = "small err"; $("out-msg").textContent = e.message; }
});

function setTemplate(t) {
  tpl = t;
  for (const c of document.querySelectorAll(".chip")) c.classList.toggle("on", c.dataset.tpl === t);
  const me = $("my-call").value.toUpperCase() || "MYCALL", to = $("tx-to").value.toUpperCase() || "CALL", grid = $("my-grid").value.toUpperCase();
  if (t === "cq") { $("tx-top").value = "CQ SSTV"; $("tx-bottom").value = [me, grid].filter(Boolean).join("  "); }
  if (t === "reply") { $("tx-top").value = `${to} de ${me}`; $("tx-bottom").value = `UR ${$("tx-rst").value || "595"}`; }
  if (t === "73") { $("tx-top").value = `${to} de ${me}`; $("tx-bottom").value = "TNX  73"; }
  draw();
}
for (const c of document.querySelectorAll(".chip")) c.addEventListener("click", () => setTemplate(c.dataset.tpl));
for (const id of ["tx-to", "tx-rst"]) $(id).addEventListener("input", () => setTemplate(tpl));
for (const id of ["tx-top", "tx-bottom"]) $(id).addEventListener("input", () => { tpl = "custom"; for (const c of document.querySelectorAll(".chip")) c.classList.toggle("on", c.dataset.tpl === "custom"); draw(); });
$("tx-mode").addEventListener("change", draw);

// draw the picture exactly as it will be sent, at the mode's size
function draw() {
  if (!last) return;
  const m = last.modes.find((x) => x.name === $("tx-mode").value) || last.modes[0];
  const cv = $("tx-canvas"), g = cv.getContext("2d");
  cv.width = m.w; cv.height = m.h;
  if (photo) { // fill the frame, cropping the overflow
    const k = Math.max(m.w / photo.width, m.h / photo.height), w = photo.width * k, h = photo.height * k;
    g.drawImage(photo, (m.w - w) / 2, (m.h - h) / 2, w, h);
  } else {
    const bg = g.createLinearGradient(0, 0, 0, m.h);
    bg.addColorStop(0, "#1B3A6B"); bg.addColorStop(1, "#0A1830");
    g.fillStyle = bg; g.fillRect(0, 0, m.w, m.h);
  }
  // Each line sits on a solid dark bar: SSTV smears fine detail, so an outline alone
  // disappears over a bright photo and the letters melt into it.
  const line = (text, top, color) => {
    if (!text) return;
    let px = Math.round(m.h * 0.135);
    do { g.font = `800 ${px}px Arial, Helvetica, sans-serif`; px -= 1; } while (g.measureText(text).width > m.w * 0.92 && px > 8);
    const bar = Math.round(px * 1.5), y = top ? 0 : m.h - bar;
    if (look.bar) { g.fillStyle = "rgba(0, 0, 0, 0.82)"; g.fillRect(0, y, m.w, bar); }
    g.textAlign = "center"; g.textBaseline = "middle"; g.lineJoin = "round";
    g.lineWidth = Math.max(3, px / 5); g.strokeStyle = "#000"; g.strokeText(text, m.w / 2, y + bar / 2 + px * 0.04);
    g.fillStyle = color; g.fillText(text, m.w / 2, y + bar / 2 + px * 0.04);
  };
  if (theirs && $("tx-inset").checked) { // their picture, bottom left, in a white frame
    const w = Math.round(m.w * 0.3), h = Math.round(w * theirs.height / theirs.width), pad = Math.round(m.w * 0.02), b = Math.max(2, Math.round(m.w / 160));
    const y = m.h - Math.round(m.h * 0.2) - h - pad;
    g.fillStyle = "#FFFFFF"; g.fillRect(pad, y, w + 2 * b, h + 2 * b);
    g.drawImage(theirs, pad + b, y + b, w, h);
  }
  line($("tx-top").value, true, look.top);
  line($("tx-bottom").value, false, look.bottom);
}

$("tx-file").addEventListener("change", async () => {
  const f = $("tx-file").files[0];
  if (!f) return;
  const img = new Image();
  img.onload = () => { photo = img; draw(); };
  img.onerror = () => { $("tx-msg").className = "small err"; $("tx-msg").textContent = "That file isn't a JPEG or PNG picture."; };
  img.src = URL.createObjectURL(f);
  api("/api/tx/photo", "POST", await f.arrayBuffer()).catch((e) => { $("tx-msg").className = "small err"; $("tx-msg").textContent = e.message; });
});

function openComposer(to, mode, t, pic) {
  theirs = null; $("tx-inset").checked = false; $("tx-inset-row").hidden = !pic;
  if (pic) { const img = new Image(); img.onload = () => { theirs = img; draw(); }; img.src = "/api/picture/" + encodeURIComponent(pic.id) + ".png"; }
  selected = ""; shown = null;
  toggleRadio(false);
  $("viewer").hidden = true; $("viewer-empty").hidden = true; $("composer").hidden = false;
  for (const b of document.querySelectorAll(".pic")) b.classList.remove("on");
  $("tx-to").value = to || "";
  if (mode && [...$("tx-mode").options].some((o) => o.value === mode)) $("tx-mode").value = mode;
  setTemplate(t);
}
$("compose-btn").addEventListener("click", () => openComposer("", "", "cq"));
$("reply-btn").addEventListener("click", () => openComposer($("viewer-call").value, shown && shown.mode, "reply", shown));
$("tx-inset").addEventListener("change", draw);
$("viewer-call").addEventListener("change", () => {
  if (!shown) return;
  shown.call = $("viewer-call").value.toUpperCase();
  api("/api/picture/call", "POST", { ID: shown.id, Call: shown.call }).catch(() => {});
});

async function stopTx(stopped) {
  if (player) { player.pause(); player = null; }
  try { render(await api("/api/tx/unkey" + (stopped ? "?stopped=1" : ""), "POST")); } catch { /* the engine's watchdog unkeys */ }
}

$("tx-go").addEventListener("click", async () => {
  const msg = $("tx-msg");
  const say = (t, bad) => { msg.className = "small " + (bad ? "err" : "muted"); msg.textContent = t; };
  if (!$("my-call").value) { toggleRadio(true); return say("Enter your callsign first (Radio, step 3): it has to be on what you send.", true); }
  $("tx-go").disabled = true;
  try {
    say("Getting the picture ready…");
    const blob = await new Promise((r) => $("tx-canvas").toBlob(r, "image/png"));
    const to = tpl === "cq" ? "" : $("tx-to").value;
    const p = await api(`/api/tx/prepare?mode=${encodeURIComponent($("tx-mode").value)}&to=${encodeURIComponent(to)}`, "POST", await blob.arrayBuffer());
    const el = new Audio(`/api/tx/audio.wav?id=${p.id}`);
    el.preload = "auto";
    await route(el);
    await new Promise((ok, bad) => { el.oncanplaythrough = ok; el.onerror = () => bad(new Error("couldn't load the audio")); el.load(); });
    render(await api(`/api/tx/key?id=${p.id}`, "POST")); // the radio is transmitting from here
    player = el;
    el.onended = () => { if (player === el) stopTx(false); };
    try { await el.play(); } catch (e) { await stopTx(true); throw new Error("couldn't play the audio: " + e.message); }
  } catch (e) { say(e.message, true); $("tx-go").disabled = false; }
});
$("tx-stop").addEventListener("click", () => stopTx(true));
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && last && last.tx.keyed) stopTx(true); });

// waterfall: 1000 to 2500 Hz, newest at the top
const wf = $("waterfall"), wctx = wf.getContext("2d");
wctx.fillStyle = "#060B14"; wctx.fillRect(0, 0, wf.width, wf.height);
setInterval(() => {
  if (!audio || document.hidden) return;
  const a = audio.analyser, bins = new Uint8Array(a.frequencyBinCount);
  a.getByteFrequencyData(bins);
  const hzPerBin = audio.ctx.sampleRate / a.fftSize;
  wctx.drawImage(wf, 0, 0, wf.width, wf.height - 1, 0, 1, wf.width, wf.height - 1);
  const row = wctx.createImageData(wf.width, 1);
  for (let x = 0; x < wf.width; x++) {
    const v = bins[Math.round((1000 + 1500 * x / wf.width) / hzPerBin)] || 0;
    const k = Math.max(0, (v - 60) / 195);
    row.data.set([20 + 235 * k * k, 40 + 160 * k, 80 + 175 * Math.sqrt(k), 255], x * 4);
  }
  wctx.putImageData(row, 0, 0);
}, 60);

poll();
loadPictures(false);
setInterval(poll, 400);
