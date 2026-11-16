# Security and review

SSTV Console is free software (AGPL-3.0), and everything is in this repository: the program,
the build, and the release process. You don't have to take anyone's word for what it does.

## What the app does

- **Network:** it makes no connections of its own except these two:
  1. To GitHub (`github.com/Jnubbz/sstv-console/releases`) to check for and download
     updates. Nothing about you is sent beyond what any web request carries. There is no
     account, telemetry, analytics or tracking.
  2. If you pick the Hamlib option, to the `rigctld` address you type in (by default
     `127.0.0.1:4532`, your own computer).
- **It serves a local page** on `127.0.0.1` only (your computer, not your network). It
  refuses requests with another host name, and every action needs a header that a web page
  on another site can't send. The page has no outside fonts, scripts or images.
- **Files:** settings, and received pictures you haven't saved yet, live in the app's config
  folder (`~/Library/Application Support` on a Mac, `%AppData%` on Windows). Pictures you
  press *Save* on are copied to `Pictures/SSTV Console`. Unsaved ones are deleted after
  7 days. The app doesn't read your other files except photos you choose to send.
- **Hardware:** the sound input and output you choose, and the serial port you choose for
  the radio. It keys your transmitter only when you send a picture or press *Tune*, and a
  watchdog unkeys it.
- **Bug reports** are never sent by the app. *Report a bug* just opens a GitHub page in your
  browser, with your version, system and radio type filled in, for you to read and submit
  or close.

## Check your download

The release publishes `latest.json`, which lists the size and SHA-256 of each zip. The file
is signed (`latest.json.sig`) with a key held only in this repo's GitHub secrets; the public
half is in `internal/selfupdate`. To compare your download:

- **Mac:** `shasum -a 256 ~/Downloads/SSTV-Console-Mac.zip`
- **Windows (PowerShell):** `Get-FileHash $HOME\Downloads\SSTV-Console-Windows.zip`

The result must match the `sha256` for your zip in `latest.json` on the same release.

## Read the code

About 5,400 lines of Go (without tests), no outside libraries (`go.mod` has no
dependencies), so there's nothing hidden in a package.

| Folder | What's in it |
| --- | --- |
| `cmd/sstv-console/` | the app: web server (`server.go`), radio control (`rig.go`), receiving (`receiver.go`), sending (`tx.go`), settings, and the web page in `web/` (plain HTML, CSS and JavaScript, no framework) |
| `sstv/` | the SSTV decoder and encoder |
| `rig/ft710/`, `rig/hamlib/` | the Yaesu CAT commands and the Hamlib (rigctld) client |
| `internal/selfupdate/` | the updater: signature check, checksum check, swap |
| `internal/serial/` | serial ports on Mac, Windows and Linux |
| `packaging/mac/shell/main.swift` | the Mac window (WebKit around the page) |
| `.github/workflows/` | the build and release: runs the tests, builds the zips, signs the manifest, publishes |

If you only have time for three things, read `internal/selfupdate/selfupdate.go` (what it
downloads and installs), `cmd/sstv-console/server.go` (what the page can ask it to do) and the
network list above (`grep -rn "net/http\|net.Dial" --include=*.go .`).

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
