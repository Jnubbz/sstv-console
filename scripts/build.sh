#!/bin/bash
# Builds the Windows and Mac (Apple Silicon) packages into dist/.
# Needs Go 1.24+. No cgo: cross-builds from Linux, Mac or Windows (Git Bash). The Mac
# window (Swift) is only built on a Mac; elsewhere the Mac app opens its page in the browser.
set -euo pipefail
cd "$(dirname "$0")/.."
VER="${VERSION:-dev}"
OUT=dist
rm -rf "$OUT"; mkdir -p "$OUT"
LDFLAGS="-s -w -X main.appVersion=$VER"

# Windows: SSTV-Console.exe (opens its own Edge app-mode window)
W="$OUT/SSTV-Console-Windows"
mkdir -p "$W"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS -H windowsgui" -o "$W/SSTV-Console.exe" ./cmd/sstv-console
cp packaging/windows/README.txt "$W/README.txt"; cp LICENSE "$W/LICENSE.txt"

# Mac: SSTV Console.app = the Swift window (packaging/mac/shell) around the engine
M="$OUT/SSTV-Console-Mac"
BUNDLE="$M/SSTV Console.app"
APP="$BUNDLE/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$APP/MacOS/sstv-console" ./cmd/sstv-console
cp packaging/mac/Info.plist "$APP/Info.plist"
if [[ "$VER" =~ ^[0-9]+\.[0-9]+\.([0-9]+) ]]; then # Finder's version: the release's
  sed -i.bak -e "s#<key>CFBundleShortVersionString</key><string>[^<]*</string>#<key>CFBundleShortVersionString</key><string>${VER%%-*}</string>#" \
    -e "s#<key>CFBundleVersion</key><string>[^<]*</string>#<key>CFBundleVersion</key><string>${BASH_REMATCH[1]}</string>#" "$APP/Info.plist"
  rm -f "$APP/Info.plist.bak"
fi
if command -v swiftc >/dev/null && [ "$(uname)" = Darwin ]; then
  swiftc -O -target arm64-apple-macos12 -framework Cocoa -framework WebKit \
    -o "$APP/MacOS/SSTV Console" packaging/mac/shell/main.swift
else
  echo "build.sh: no Swift here: the Mac app opens in the browser (build on a Mac for the window)" >&2
  sed -i.bak -e "s#<key>CFBundleExecutable</key><string>[^<]*</string>#<key>CFBundleExecutable</key><string>sstv-console</string><key>LSUIElement</key><true/>#" "$APP/Info.plist"
  rm -f "$APP/Info.plist.bak"
fi
if command -v codesign >/dev/null; then
  codesign --force --deep --sign - "$BUNDLE" # ad hoc: seals the bundle (not Apple-notarized)
fi
cp packaging/mac/README-Mac.txt "$M/"; cp LICENSE "$M/LICENSE.txt"

(cd "$OUT" && for d in SSTV-Console-Windows SSTV-Console-Mac; do zip -qry "$d.zip" "$d"; done)
ls -la "$OUT"
