#!/usr/bin/env bash
# Remove Recap.app and the CLI symlink. Never touches ~/.recap or user repos.
#   sudo packaging/macos/uninstall.sh
set -euo pipefail

APP="/Applications/Recap.app"
LINK="/usr/local/bin/recap"

pkill -x Recap >/dev/null 2>&1 || true

if [ -L "$LINK" ]; then
    case "$(readlink "$LINK")" in
        "$APP"/*) rm -f "$LINK"; echo "removed $LINK" ;;
        *) echo "kept $LINK (does not point into Recap.app)" ;;
    esac
fi

if [ -d "$APP" ]; then
    rm -rf "$APP"
    echo "removed $APP"
fi

pkgutil --forget dev.recap.pkg >/dev/null 2>&1 || true
echo "Recap uninstalled. Your recap data and recipe repos were left untouched."
