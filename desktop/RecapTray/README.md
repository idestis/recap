# RecapTray — macOS menu bar + overlay prototype

SwiftUI app with the recap tray menu and the ⌥⇧Space overlay (SPEC D-21–D-23). It holds no logic: it calls the `recap` CLI.

> Written against macOS 14+ APIs but **not yet compiled**: it was authored without a Mac. Expect a few compiler fixes on first build.

## Run

```sh
task build                      # from repo root: builds bin/recap
export RECAP_BIN="$PWD/bin/recap"
cd desktop/RecapTray
swift build && swift run        # run from this folder (Info.plist is linked by relative path)
```

The loop icon appears in the menu bar. Press **⌥⇧Space** anywhere to open the overlay.

## What's in it

- `App.swift` — app entry, ⌥⇧Space hotkey (Carbon, no Accessibility permission), CLI bridge, status polling (`recap status --json` every 2 s).
- `Tray.swift` — menu bar icon drawn in code (idle loop as template image; recording adds the red dot), tray menu with sources, Add context, Stop and review.
- `Overlay.swift` — floating glass panel near the bottom of the screen: text field, attachment chips, "Detected:" line, + button, session chip, mic hint, Sunrise send button; contextual pills below (Screenshot of <App> ⌘⇧S, Area ⌘⇧A, <Browser> tab link ⌘⇧L); drag and drop files.

## Permissions you'll see

- **Screen Recording** — first time you use Screenshot or Area.
- **Automation** — first time you use Tab link in each browser (Safari, Chrome, Arc, Brave, Edge).

## Next steps for the MVP

- Move to an Xcode project or app bundle with an asset catalog (Sunrise icon from `/icons`), signing and notarization.
- Talk to the `recapd` socket instead of spawning the CLI.
- Stop review window (related / maybe / excluded).
- On macOS 26, replace `.ultraThinMaterial` with Liquid Glass.
- Icon motion: blink on start, one turn on capture, spin while drafting.
