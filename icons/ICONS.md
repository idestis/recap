# recap icons — "Sunrise"

| File | Use |
|---|---|
| `recap-app-icon.svg` | App icon, Dock, Homebrew cask, GitHub avatar. Export to `AppIcon.appiconset` / `.icns`; a designer should fit it to Apple's icon grid before release. |
| `recap-mark-light-bg.svg` | Logo mark on light backgrounds (README, website). |
| `recap-mark-dark-bg.svg` | Logo mark on dark backgrounds. |
| `menubar-idle-template.svg` | Menu bar, idle. Load as a template image so macOS tints it. |
| `menubar-dot-red.svg` | REC dot only, to layer over the template loop while recording. |
| `menubar-rec-light.svg` / `menubar-rec-dark.svg` | Pre-composed recording icons for light and dark menu bars. |

Colors: Sunrise gradient `#FFBE55 → #FF5A47`, loop `#FFFFFF`, dot `#1A212C`, REC red `#E5484D`, brand amber `#F2B33D`.

Menu bar states: idle = loop · recording = loop + red dot (blinks 2–3 times on start and when recap needs you) · captured = one quick turn · processing = continuous spin · done = brief checkmark. Respect Reduce Motion. The gradient is for the app icon and branding only; menu bar icons stay single-color.
