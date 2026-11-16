# Security and review

SSTV Console is free software (AGPL-3.0), and everything is in this repository: the program,
the build, and the release process. You don't have to take anyone's word for what it does.

## What the app does

- **Network:** it makes no connections of its own except these two:
  1. To GitHub (`github.com/Jnubbz/sstv-console/releases`, which hands downloads on to its
     own file servers) to look for a new version a few seconds after starting, every
     6 hours, and when you press *Check for updates*; and to download one when you press
     *Install and restart*. Nothing about you is sent beyond what any web request carries.
     There is no account, telemetry, analytics or tracking.
  2. If you pick the Hamlib option, to the `rigctld` address you type in (by default
     `127.0.0.1:4532`, your own computer).
- **The window:** on a Mac it's Apple's WebKit, showing only the local page. On Windows it's
  Microsoft Edge in app mode, with a profile of its own, so nothing is shared with your
  everyday Edge (no sign-in, history or extensions). The page makes no outside requests;
  whatever Edge does in the background on its own follows your Edge and Windows settings.
- **It serves a local page** on `127.0.0.1` only (your computer, not your network). It
  refuses requests addressed to any other host name, requests the browser says come from
  another site, and any action that doesn't carry a header a web page on another site can't
  send. Its content policy lets the page load only the app's own files: no outside fonts,
  scripts or images.
- **Files:** settings, the pictures you've received but not saved, and the photos you've
  used (the last 40, kept as they were so you can pick them again) live in a folder called
  `SSTV Console` in the app's config folder (`~/Library/Application Support` on a Mac,
  `%AppData%` on Windows). On a Mac the app makes it readable by your account only; on
  Windows `%AppData%` is already per user. Pictures you press *Save* on are copied to
  `Pictures/SSTV Console`, and *Record 60 seconds* writes its WAV file there. Unsaved
  pictures are deleted after 7 days. The app can't browse your disk: a photo reaches it only
  when you pick it in the file chooser.
- **Hardware:** the sound input and output you choose, and the serial port you choose for
  the radio. It keys your transmitter only when you send a picture or press *Tune*. A
  watchdog unkeys it when the picture should be over, and if the radio doesn't let go the
  app says so on screen and keeps trying until it does. If the app itself were killed
  mid-picture, only the radio's own transmit time-out timer would stop the carrier. Most
  radios have one (on the FT-710: *Operation Setting → General → TX Time Out Timer*): set it
  a little longer than your longest picture (Scottie DX takes about 4½ minutes).
- **Updates** replace only the app itself (the `.app` on a Mac, the files in the app's
  folder on Windows), working in a temporary `.sstv-update-<number>` folder next to it, and
  only after the signed manifest and the zip's SHA-256 both check out (see below). Nothing is
  installed until you press *Install and restart*.
- **Bug reports** are never sent by the app. *Report a bug* just opens a GitHub page in your
  browser, with your version, system, radio model and whether audio is on filled in (no
  callsign, no pictures), for you to read and submit or close.

## Check your download

The release publishes `latest.json`, which lists the size and SHA-256 of each zip. The file
is signed (`latest.json.sig`) with a key held only in this repo's GitHub secrets; the public
half is in `internal/selfupdate`. To compare your download:

- **Mac:** `shasum -a 256 ~/Downloads/SSTV-Console-Mac.zip`
- **Windows (PowerShell):** `Get-FileHash $HOME\Downloads\SSTV-Console-Windows.zip`

The result must match the `sha256` for your zip in `latest.json` on the same release.

## Read the code

About 5,600 lines of Go (without tests), no outside libraries (`go.mod` has no
dependencies), so there's nothing hidden in a package.

| Folder | What's in it |
| --- | --- |
| `cmd/sstv-console/` | the app: web server (`server.go`), radio control (`rig.go`), receiving (`receiver.go`), sending (`tx.go`), settings, and the web page in `web/` (plain HTML, CSS and JavaScript, no framework) |
| `sstv/` | the SSTV decoder and encoder |
| `rig/ft710/`, `rig/hamlib/` | the Yaesu CAT commands (the FT-710's, which the FT-991A, FT-991, FT-891 and FTDX10 share) and the Hamlib (rigctld) client |
| `internal/selfupdate/` | the updater: signature check, checksum check, swap |
| `internal/serial/` | serial ports on Mac, Windows and Linux |
| `internal/signmanifest/` | signs `latest.json` for a release (run by the release workflow) |
| `cmd/sstv-file/` | a command-line tool that runs the decoder and encoder on WAV and PNG files |
| `packaging/mac/shell/main.swift` | the Mac window (WebKit around the page) |
| `scripts/` | `build.sh` (the zips) and `make-icons.py` (the icon files) |
| `.github/workflows/` | the build and release: runs the tests, builds the zips, signs the manifest, publishes |

If you only have time for three things, read `internal/selfupdate/selfupdate.go` (what it
downloads and installs), `cmd/sstv-console/server.go` (what the page can ask it to do) and the
network list above (`grep -rn "net/http\|net.Dial" --include=*.go .`).

## Files that aren't source code

A few files in the repository are binary. They hold pictures and fonts, not program code,
and you can check each against its source:

| Files | What they are | How to check |
| --- | --- | --- |
| `packaging/icon/AppIcon.icns`, `packaging/icon/AppIcon.ico`, `cmd/sstv-console/rsrc_windows_amd64.syso` | the Mac and Windows icons (the `.syso` is the icon as a Windows resource, which the Go linker puts into the `.exe`) | `python3 scripts/make-icons.py` rebuilds all three from `packaging/icon/icon.svg` (it needs `rsvg-convert`). With librsvg 2.58 the result is byte for byte the same; another version may differ by a few pixels. |
| `cmd/sstv-console/web/fonts/*.woff2` | the Inter and Inconsolata fonts the page uses, with their licences (SIL Open Font License) | the same files as in the npm packages `@fontsource/inconsolata` and `@fontsource-variable/inter` (version 5.3.0): run `npm pack` on each, unpack it and `cmp` the files in its `files/` folder |

## Build it yourself

You don't have to run our build at all. With Go 1.24 or newer installed:

    git clone https://github.com/Jnubbz/sstv-console
    cd sstv-console
    go test ./...
    go run ./cmd/sstv-console

`scripts/build.sh` makes the same zips the release does. A build you make yourself runs
without the Mac or Windows warning, because your own computer trusts what you built.
(A self-built copy shows as a "development build" and doesn't update itself.)

## Reporting a security problem

Please use this repository's **Security** tab → **Report a vulnerability**, so it stays private
until it's fixed. If that button isn't there, open an issue that says you have a security
report, without details, and I'll get in touch. Ordinary bugs: About → *Report a bug*.
