<p align="center"><img src="icons/recap-app-icon.svg" width="96" alt="recap"></p>

# recap

**Fix it once. Never figure it out again.**

recap captures what you and your AI agents do during an incident (terminal tabs, Claude Code, notes, links, screenshots, and later Slack and call transcripts) and turns the parts you approve into clean, risk-labeled Markdown **recipes** in your own Git repo. Next time, you or an agent find the recipe and run it with a preflight instead of figuring it out again.

recap is local first, brings your own model, and is not an incident management tool. It works next to the tools you already use.

> **Status: early development.** The CLI works end to end (sessions, tab join/leave, capture, redaction, risk, model drafting, dedup, publish, apply, find, run with preflight, Claude Code guardrail). The macOS menu bar app and `.pkg` installer build from source. Website: <https://idestis.github.io/recap/>

---

## Install

**macOS app + CLI:** `task pkg && task pkg:install` (or double-click `dist/Recap-<version>.pkg`). Installs `/Applications/Recap.app` (menu bar + ⌥⇧Space overlay) and links `/usr/local/bin/recap`.

**CLI only:** `task build` → `bin/recap` (Go 1.26+, macOS or Linux, zsh or bash). Requires [Task](https://taskfile.dev).

```sh
cd ~/runbooks && recap init                          # this folder becomes a space (like git init)
recap init --name team --remote git@github.com:acme/runbooks.git \
           --publish pr --path ~/code/acme           # clones into ./runbooks; PRs; picked when you work in ~/code/acme
eval "$(recap shell-init zsh)"                       # or bash; add to your shell rc
recap models set --provider ollama --model llama3.1:8b   # optional: local model drafting
recap hooks install                                  # optional: Claude Code capture + guardrail
```

### Spaces

One recap install serves many projects. Each **space** is a recipe Git repo with its own publish strategy, stored in `~/.recap/config.yaml`:

| `publish` | for | what `recap stop --publish` / `recap publish` does |
|---|---|---|
| `pr` | team repo | branch `recap/<handle>-<name>`, push, open a PR with `gh` |
| `push` | private repo | commit the session folder and push |
| `none` | anything | write files only |

The space is chosen by `--space`, then `RECAP_SPACE`, then the working directory (`--path`), then the default (`recap spaces use <name>`).

### Models

Any OpenAI-compatible API: `recap models set --provider openai --model gpt-4.1-mini [--base-url https://…/v1]`, then `recap models key set` (Keychain). Local: `--provider ollama`. `recap models test` checks it. Models only reword the draft after redaction; commands and risk floors come from rules.

Then:

```console
$ recap start "checkout API returning 502s"
● Recording  2026-10-22.1  checkout-api-returning-502s
  kube prod-eu
  New tab? run: recap join   ·   add context: recap ctx "…"

$ kubectl get pods -n checkout
$ kubectl set resources deploy/checkout-api -n checkout --limits=memory=1Gi
$ recap ctx "rollback ruled out, migration 0142 already ran https://grafana.acme.io/d/checkout"
✓ Note added · decision
✓ Dashboard linked https://grafana.acme.io/d/checkout

$ recap stop
  Related    4 2 commands (1 changes) · 1 notes · 1 links/files
  Excluded   0 failed commands
? Did this fix it? [y/N] › y
✓ Draft: "Checkout API returning 502s" · risky-prod · 2 steps
✓ Wrote sessions/2026-10-22.1-checkout-api-returning-502s/
  Review delta.md, then: recap apply 2026-10-22.1

$ recap apply 2026-10-22.1
✓ Added recipes/checkout-api-returning-502s.md
✓ Removed session folder (still in Git history)

$ recap find "pods out of memory"
$ recap run "pods out of memory"      # by name or symptom; asks for {namespace}, {deployment}, …
```

Desktop prototype: see [desktop/RecapTray/README.md](desktop/RecapTray/README.md).

## How it works

```
capture → redact → risk rules → draft → dedup → review → session folder → apply → recipes/
```

- **Capture:** attached terminal tabs (`recap join` / `recap leave`), Claude Code hooks, `recap ctx` and the ⌥⇧Space overlay. Slack, Wispr Notetaker transcripts and Git commits are pulled at stop (v0.2).
- **Redact:** secrets are masked locally before anything is drafted or written.
- **Risk:** deterministic rules label steps safe / risky / destructive, and prod. A model may only raise risk.
- **Draft:** templates today; your own model (OpenAI-compatible or Ollama) in the MVP.
- **Dedup:** a close match becomes a variant of an existing recipe, never a duplicate, and staging fixes never merge into prod recipes.
- **Apply:** sessions are temporary changes (like OpenSpec); recipes are the source of truth.

## Commands

| | |
|---|---|
| `recap init`, `spaces`, `shell-init`, `hooks install`, `models` | setup |
| `recap start`, `join`, `leave`, `status`, `ctx`, `timeline`, `stop` | sessions |
| `recap find`, `run`, `apply`, `publish`, `prune` | recipes |
| `recap mcp`, `slack`, `wispr`, `github` | agents and integrations (planned) |
| `--json`, `--yes`, `--dry-run`, `--space` | for agents and CI |


## Repo layout

```
cmd/recap/            CLI entry
internal/cli/         commands (Cobra)
internal/session/     model, ULID, CalVer handles, naming
internal/store/       local store under ~/.recap (prototype: files; MVP: daemon + SQLite)
internal/config/      ~/.recap/config.yaml: spaces, model
internal/llm/         OpenAI-compatible client (Ollama, OpenAI, …), Keychain keys
internal/draft/       model-assisted drafting (prompts/)
internal/publish/     pr / push / none per space
internal/ctxdetect/   one-input type detection
internal/redact/      secret masking
internal/risk/        risk rules
internal/recipe/      draft, dedup, session folders, apply, find, prune
internal/shell/       zsh/bash integration scripts
desktop/RecapTray/    SwiftUI menu bar + overlay
packaging/macos/      Recap.app + .pkg build scripts
scripts/smoke.sh      end-to-end test with a local model
landing/              website (GitHub Pages)
e2e/                  end-to-end tests with a local model
icons/                Sunrise icon set
```

## Privacy

Secrets are masked locally before any model call or Git write. Raw timelines, screenshots and transcripts stay on your machine; only reviewed recipes and short session summaries go to Git. recap never records audio and never deletes anything on its own.

## License

Apache-2.0. See [LICENSE](LICENSE).
