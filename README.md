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
- [x] SSTV decoder and encoder (`sstv/`): Scottie 1/2/DX, Martin 1/2, Robot 36/72,
      PD120/180/240, with header (VIS) detection, off-tune correction and slant correction.
      `cmd/sstv-file` runs it on WAV files
- [x] Radio panel: pick the radio's sound input, connect CAT (frequency and mode in the top bar)
- [x] Live receive: waterfall, the picture as it arrives, finished pictures saved to
      `Pictures/SSTV Console` and listed
- [x] Tune button: a 10-second steady carrier for the antenna tuner
- [x] Band buttons: one click to each band's SSTV frequency, switching the radio to DATA-U (DATA-L on 80 and 40 m) from whatever mode it was in
- [x] Locks onto a picture already under way (no start tones needed)
- [x] Transmit: compose a picture on your photo with CQ / reply / 73 templates, keyed over
      CAT with a watchdog; sent pictures are listed with the received ones
- [ ] Conversation threads per station, with the callsign read from the picture or FSK ID
- [ ] PD50 / PD90 / PD160 / PD290

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
