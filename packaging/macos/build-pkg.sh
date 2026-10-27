#!/usr/bin/env bash
# Build dist/Recap-<VERSION>.pkg from dist/Recap.app. Run from the repo root
# after build-app.sh.
#   VERSION=1.2.3 [OUT=dist] [SIGN_PKG_IDENTITY=...] [NOTARY_PROFILE=...] packaging/macos/build-pkg.sh
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
OUT="${OUT:-dist}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HERE="$ROOT/packaging/macos"
APP="$OUT/Recap.app"
FINAL="$OUT/Recap-$VERSION.pkg"
[ -d "$APP" ] || { echo "missing $APP: run packaging/macos/build-app.sh first" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/root/Applications"
ditto --noextattr --noqtn --norsrc "$APP" "$WORK/root/Applications/Recap.app"

xattr -cr "$WORK/root" || true
COPYFILE_DISABLE=1 pkgbuild --root "$WORK/root" \
    --identifier dev.recap.pkg \
    --version "$VERSION" \
    --install-location / \
    --scripts "$HERE/scripts" \
    --ownership recommended \
    "$WORK/component.pkg"

sed "s/@VERSION@/$VERSION/g" "$HERE/distribution.xml" > "$WORK/distribution.xml"
mkdir -p "$OUT"
rm -f "$FINAL"

if [ -n "${SIGN_PKG_IDENTITY:-}" ]; then
    productbuild --distribution "$WORK/distribution.xml" --package-path "$WORK" \
        --sign "$SIGN_PKG_IDENTITY" "$FINAL"
else
    productbuild --distribution "$WORK/distribution.xml" --package-path "$WORK" "$FINAL"
fi

if [ -n "${NOTARY_PROFILE:-}" ]; then
    xcrun notarytool submit "$FINAL" --wait --keychain-profile "$NOTARY_PROFILE"
    xcrun stapler staple "$FINAL"
fi
echo "Built $FINAL"
