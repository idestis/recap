# CLAUDE.md

Guidance for Claude Code and other agents working in this repo. Decisions are referenced in code as `SPEC D-xx`. `docs/SPEC.md` and `docs/WORKFLOWS.md` are local-only for now (gitignored) and will move to GitHub Project items; if you have them, read them before changing behavior, and if a change contradicts a decision, ask first.

## Current state

- **CLI (Go): works end to end.** Context (e.g. Kubernetes) comes only from recorded commands (`kubectx`, `kubectl config use-context`, `--context`); sessions never read live state. Only the Claude Code PreToolUse guard reads the live kubeconfig, because it judges the command about to run. Sessions with CalVer handles and ULIDs, zsh/bash join/leave, `ctx` type detection, timeline, stop with interactive review (huh) or flags, redaction, risk rules, template drafting + optional model drafting (Ollama / OpenAI-compatible, `internal/llm`, `internal/draft`), dedup into variants, **spaces** (many recipe repos, chosen by path; `internal/config`), **publishing** per space (`pr` / `push` / `none`; `internal/publish`), apply/keep/prune, find, run with preflight (by name or symptom, asks for vars), Claude Code hooks and `recap skill` (recipe → Claude Code skill).
- **Desktop (SwiftUI):** builds universal. Tray (status, space picker, launch at login, install CLI, stop → publish) + ⌥⇧Space overlay for quick context with dictation. Calls the bundled CLI.
- **Packaging:** `task pkg` → `dist/Recap-<version>.pkg` (Recap.app + universal CLI, postinstall links `/usr/local/bin/recap`). Unsigned unless signing env vars are set.
- **Also done:** `recap mcp` (stdio, five tools; `internal/mcp`), fish, stop review related/maybe/excluded (`internal/review`), embedding dedup with jaccard fallback, Claude Code Stop hook records transcript intent (`internal/transcript`).
- **Stubs:** `slack|github login`.

## Build and test

```sh
task build          # → bin/recap (CGO_ENABLED=0, version via ldflags)
task check          # gofmt + vet + test
task desktop        # swift build (debug)
task pkg            # universal CLI + Recap.app + .pkg in dist/
task test:e2e       # e2e: real binary + Ollama drafts, apply by name, run with vars (RECAP_E2E_MODEL=llama3.1:8b)
task ollama:smoke   # quick scripted smoke run incl. publish by push (MODEL=llama3.1:8b)
task landing:dev    # website (Vite + Tailwind) at http://localhost:5173/recap/
task landing:check  # build the site and fail on any third-party request
```

Use throwaway dirs when testing by hand (see `scripts/smoke.sh`):
`RECAP_HOME=/tmp/recap-home RECAP_REPO=/tmp/runbooks recap …`
Git tests set `GIT_CONFIG_GLOBAL` to an empty file so user signing config doesn't interfere.

## Stack

| Area | Now | MVP target |
|---|---|---|
| Language | Go, `CGO_ENABLED=0` | same |
| CLI | Cobra (plain help) + `huh` only for choices behind `interactive()` | same; no Charm-styled help |
| State | files under `RECAP_HOME`, default `~/.recap` (`internal/store`, `config.yaml`) | `recapd` daemon, Unix socket, `modernc.org/sqlite` |
| Drafting | templates + OpenAI-compatible/Ollama (`internal/llm`, `internal/draft`) | more model tasks (naming, classify, compare) |
| Dedup | embeddings (OpenAI-compatible `/v1/embeddings`, cosine in Go) gated by tool + prod class; token overlap fallback | same, calibrated threshold |
| Secrets | model keys via `zalando/go-keyring` | + Slack/GitHub tokens |
| MCP | official MCP Go SDK, stdio | same, via daemon |
| Desktop | SwiftUI, calls CLI | calls daemon socket; Liquid Glass on macOS 26 |
| Release | Taskfile + `packaging/macos` (.pkg) | GoReleaser, signed + notarized, Homebrew formula + cask |

## Non-negotiable invariants

1. Redaction runs locally before any model call, naming, or Git write (D-11).
2. Risk is rule-based; a model may raise it, never lower it (D-12).
3. No destructive action without a human; risky steps ask unless `--yes` (D-26).
4. recap never deletes on its own; `prune` only when a person runs it (D-16).
5. Raw timelines, screenshots and transcripts never go into Git (D-16).
6. Secrets live in the Keychain, never in config, logs or the store (D-15).
7. ULID and handle never change; names and links can (D-07, D-08).
8. Store external IDs (Slack channel `C…`), not names (D-08).
9. `recap start` is instant and offline, and records rather than guesses: no live kubeconfig/cloud state is read for a session (D-05).
10. Every prompt has a non-interactive path: `--yes`, `--json`, `--dry-run`.
11. Pull, don't record: recap never captures audio (D-20).
12. Hooks must never break the user's shell or Claude Code: on internal errors, exit 0 silently (except the deliberate exit 2 block).
13. The desktop app holds no business logic (D-04).

## Conventions

- Business logic in `internal/*`, never in `internal/cli`; the TUI and `--json` paths call the same functions.
- Wrap errors with context; pass `context.Context` once the daemon exists.
- Recipe Markdown output must be deterministic for reviewable diffs.
- Code comments reference decisions as `SPEC D-xx`.
- Tests: table tests for risk, corpus tests for redaction, golden files for recipe rendering and delta apply (add `testdata/`).

## MVP task list (suggested order)

1. Click-test the tray app (hotkey, overlay, dictation, stop → publish); signing + notarization with a Developer ID.
2. Per-model embedding thresholds (0.77 is calibrated for nomic-embed-text only; rerun with `RECAP_CALIBRATE_MODEL=<model> go test -tags calibrate -run TestCalibrate -v ./internal/recipe/`).
3. Tray "add context" for any active session from the space picker; tray reads the stop `maybe` count.
4. `recapd` daemon + SQLite store behind the current `store` API (covered by `internal/store` tests).
5. GoReleaser, signing, notarization, Homebrew formula.

Then v0.2 (Slack, GitHub, infra context, screenshot OCR + blur) (tracked as GitHub Project items).

## Out of scope

Incident channel creation, paging, status pages, postmortem tooling, speech-to-text, any hosted recap service.
