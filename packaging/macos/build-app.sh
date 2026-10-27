#!/usr/bin/env bash
# Assemble dist/Recap.app. Run from the repo root.
#   RECAP_BIN=<built recap CLI> [VERSION=1.2.3] [OUT=dist] [SIGN_APP_IDENTITY=...] packaging/macos/build-app.sh
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
OUT="${OUT:-dist}"
: "${RECAP_BIN:?RECAP_BIN must point to a built recap CLI binary}"
[ -x "$RECAP_BIN" ] || { echo "RECAP_BIN not executable: $RECAP_BIN" >&2; exit 1; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HERE="$ROOT/packaging/macos"
PKG_DIR="$ROOT/desktop/RecapTray"
APP="$OUT/Recap.app"
BUILD="${VERSION%%-*}"            # CFBundleVersion: numeric part only
[ -n "$BUILD" ] || BUILD="0"

echo "==> Building Swift app (release)"
if swift build -c release --package-path "$PKG_DIR" --arch arm64 --arch x86_64; then
    BIN_DIR="$(swift build -c release --package-path "$PKG_DIR" --arch arm64 --arch x86_64 --show-bin-path)"
else
    echo "warning: universal build failed, falling back to host architecture" >&2
    swift build -c release --package-path "$PKG_DIR"
    BIN_DIR="$(swift build -c release --package-path "$PKG_DIR" --show-bin-path)"
fi

echo "==> Assembling $APP"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources"
cp "$BIN_DIR/Recap" "$APP/Contents/MacOS/Recap"
cp "$RECAP_BIN" "$APP/Contents/Helpers/recap"
chmod 755 "$APP/Contents/MacOS/Recap" "$APP/Contents/Helpers/recap"
sed -e "s/@VERSION@/$VERSION/g" -e "s/@BUILD@/$BUILD/g" "$HERE/Info.plist" > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist" >/dev/null

echo "==> App icon"
SVG="$ROOT/icons/recap-app-icon.svg"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PNG="$WORK/icon-1024.png"
if command -v rsvg-convert >/dev/null 2>&1 && rsvg-convert -w 1024 -h 1024 "$SVG" -o "$PNG" 2>/dev/null; then
    :
elif qlmanage -t -s 1024 -o "$WORK" "$SVG" >/dev/null 2>&1 && [ -f "$WORK/$(basename "$SVG").png" ]; then
    mv "$WORK/$(basename "$SVG").png" "$PNG"
elif sips -s format png -z 1024 1024 "$SVG" --out "$PNG" >/dev/null 2>&1 && [ -s "$PNG" ]; then
    :
else
    rm -f "$PNG"
fi
if [ -s "$PNG" ]; then
    SET="$WORK/AppIcon.iconset"
    mkdir -p "$SET"
    for s in 16 32 128 256 512; do
        sips -z "$s" "$s" "$PNG" --out "$SET/icon_${s}x${s}.png" >/dev/null
        sips -z $((s * 2)) $((s * 2)) "$PNG" --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
    done
    iconutil -c icns "$SET" -o "$APP/Contents/Resources/AppIcon.icns"
else
    echo "warning: could not render $SVG (no rsvg-convert, qlmanage or sips support); skipping app icon" >&2
fi

echo "==> Codesign"
if [ -n "${SIGN_APP_IDENTITY:-}" ]; then
    codesign --force --options runtime --timestamp -s "$SIGN_APP_IDENTITY" "$APP/Contents/Helpers/recap"
    codesign --force --options runtime --timestamp -s "$SIGN_APP_IDENTITY" "$APP/Contents/MacOS/Recap"
    codesign --force --options runtime --timestamp -s "$SIGN_APP_IDENTITY" "$APP"
else
    codesign --force --deep -s - "$APP"
fi
codesign --verify --verbose=1 "$APP"
echo "Built $APP ($VERSION)"
