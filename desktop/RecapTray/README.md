# Recap.app — menu bar app and quick context overlay

SwiftUI menu bar app for recap. It holds no logic: every action calls the bundled `recap` CLI (SPEC D-04), so the terminal and the app always agree.

## Build and run

```sh
task desktop                     # swift build (debug)
task app                         # dist/Recap.app with the universal CLI in Contents/Helpers
task pkg                         # dist/Recap-<version>.pkg (unsigned unless signing env vars are set)
open dist/Recap.app --args --show-settings   # or --show-overlay, handy while developing
```

The loop icon appears in the menu bar. While a session records, a red dot inside it blinks.

## What's in it

- `App.swift` — app entry, native `MenuBarExtra` menu, launch arguments.
- `Tray.swift` — menu bar icon drawn in code (template loop when idle; loop + blinking red dot while recording), native tray menu: session status, Add Context…, Stop and Review…, Space picker, Settings…, Quit. Stop asks "Did this fix it?", runs `recap stop`, then offers Show in Finder and Publish.
- `Overlay.swift` — the quick context bar (default ⌥⇧Space): glass input that takes focus immediately so macOS Dictation and Wispr Flow can type into it, mic button, attach (+) and drag-and-drop, session pill above the bar, contextual pills below (Screenshot of <App> ⌘⇧S, Area ⌘⇧A, <Browser> tab link ⌘⇧L). With no session running, the text starts one (`recap start`).
- `Settings.swift` — glass settings panel: Launch at login, command line tool link, recordable shortcut, spaces, model.
- `HotKey.swift` — global shortcut via Carbon (no Accessibility permission), stored in UserDefaults.
- `Glass.swift` — Liquid Glass on macOS 26, material fallback on 14–15.
- `CLI.swift` — finds the CLI (bundled `Contents/Helpers/recap`, then `RECAP_BIN`, then PATH) and polls `recap status --json`.

## Permissions you'll see

- **Screen Recording** — first time you use Screenshot or Area.
- **Automation** — first time you use Tab link in each browser (Safari, Chrome, Arc, Brave, Edge).
- **Administrator password** — only if you choose "Install command line tool" in Settings.
