# SSTV Console

Slow-scan TV that works like a messaging app. Pictures you receive are filed under the
station that sent them, and replying takes one click: their callsign, RST and name go onto
your picture automatically. For Windows and Mac, free software under the AGPL-3.0.

By KN0BLE. A sister project to [CW Console](https://github.com/Jnubbz/cw-console), built the
same way so the two can merge into one app later.

**Download:** the latest Windows and Mac builds are on the
[Releases page](https://github.com/Jnubbz/sstv-console/releases/latest). Once installed, the
app updates itself.

## Install

- **Mac (Apple Silicon):** unzip `SSTV-Console-Mac.zip`, drag *SSTV Console.app* to
  Applications, and the first time right-click it and choose Open.
- **Windows:** unzip `SSTV-Console-Windows.zip` somewhere you can write to (Documents or
  the Desktop, not Program Files) and run `SSTV-Console.exe`.

That's the only manual install: after that, the app updates itself.

## Status

Early days. In place now:

- [x] The app shell: Windows (Edge app-mode window) and Mac (native window) around one Go engine
- [x] Signed self-updates from the first release (below)
- [ ] SSTV receive: VIS detection, Scottie / Martin / PD / Robot decoders, slant correction
- [ ] Inbox and conversation threads, with the callsign read from the FSK ID or the picture
- [ ] Quick replies: templates with callsign, RST and name filled in
- [ ] SSTV transmit and FT-710 rig control

## How updates work

Every merge to `main` publishes release `v0.1.<run>` (`.github/workflows/release.yml`)
with the Windows and Mac zips and a manifest, `latest.json`, that names each zip's size
and SHA-256. The manifest is signed with an Ed25519 key that exists only as the
repository secret `UPDATE_SIGNING_KEY`; the app carries the public half
(`internal/selfupdate`). The app checks a few seconds after starting and every 6 hours
(or About > Check for updates), and installs only if the signature checks out **and** the
download matches the manifest.

- **Mac:** the new `SSTV Console.app` is unpacked next to the old one and swapped in by
  renames, then reopened.
- **Windows:** each file is renamed to `*.old` and the new one put in its place (Windows
  can rename a running .exe but not overwrite it); the new version takes over the same
  window and the page reloads.

Development builds (`dev`) never update themselves.

## Building

Go 1.24, no cgo, no outside modules: `scripts/build.sh` cross-builds both zips into
`dist/`. The Mac window is Swift, so build on a Mac for it; elsewhere the Mac app opens
its page in the browser.

    go test ./...
    go run ./cmd/sstv-console      # opens the page in your browser
