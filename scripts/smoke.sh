#!/usr/bin/env bash
# End-to-end smoke test in throwaway dirs: two spaces, one session captured
# via the shell hook, drafted by a local Ollama model, published by push.
#   scripts/smoke.sh [ollama-model]
set -euo pipefail

MODEL="${1:-llama3.1:8b}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RECAP="$ROOT/bin/recap"
TMP="$(mktemp -d -t recap-smoke)"
trap 'rm -rf "$TMP"' EXIT

export RECAP_HOME="$TMP/home" KUBECONFIG="$TMP/kube/config" NO_COLOR=1
export GIT_CONFIG_GLOBAL="$TMP/gitconfig" GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=smoke GIT_AUTHOR_EMAIL=smoke@example.com GIT_COMMITTER_NAME=smoke GIT_COMMITTER_EMAIL=smoke@example.com
: > "$GIT_CONFIG_GLOBAL"
mkdir -p "$TMP/kube" "$TMP/code/acme"
printf 'current-context: prod-eu\n' > "$KUBECONFIG"

# A private repo with a bare origin (push strategy) and a team space (pr).
git init -q --bare -b main "$TMP/private.git"
git clone -q "$TMP/private.git" "$TMP/private" 2>/dev/null
git -C "$TMP/private" commit -q --allow-empty -m init && git -C "$TMP/private" push -q origin HEAD:main

"$RECAP" init --name mine --repo "$TMP/private" --publish push --base main
"$RECAP" spaces add team --repo "$TMP/team" --publish none --path "$TMP/code/acme"
"$RECAP" spaces
(cd "$TMP/code/acme" && "$RECAP" spaces --json | grep -q '"resolved": "team"') && echo "✓ path selects team space"

if curl -fsS http://localhost:11434/v1/models >/dev/null 2>&1; then
  "$RECAP" models set --provider ollama --model "$MODEL" --timeout 300
  "$RECAP" models test
else
  echo "! Ollama not running — drafting with templates"
fi

"$RECAP" start "checkout API returning 502s" --space mine
H="$("$RECAP" _active)"
hook() { "$RECAP" _hook cmd --session "$H" --exit "$1" --dir "$TMP" --tty ttys001 -- "$2"; }
hook 0 "kubectl get pods -n checkout"
hook 1 "kubectl logs deploy/checkout-api -n checkout --previous --tokn"
hook 0 "kubectl describe pod checkout-api-7d9f-abcde -n checkout"
hook 0 "kubectl set resources deploy/checkout-api -n checkout --limits=memory=1Gi"
hook 0 "kubectl rollout status deploy/checkout-api -n checkout"
echo "rollback ruled out, migration 0142 already ran https://grafana.example.com/d/checkout" | "$RECAP" ctx --source overlay -
echo "export API_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789" | "$RECAP" ctx -
"$RECAP" status

"$RECAP" stop --fixed --publish --json | tee "$TMP/stop.json"
grep -q '"pushed": true' "$TMP/stop.json" && echo "✓ published by push"
git -C "$TMP/private.git" log --format='%s%n%b' -1 main
DELTA="$(git -C "$TMP/private.git" show main --name-only --format= | grep delta.md)"
git -C "$TMP/private.git" show "main:$DELTA"; git -C "$TMP/private.git" show "main:${DELTA%delta.md}recipe.md"
if git -C "$TMP/private.git" show "main:${DELTA%delta.md}" && git -C "$TMP/private.git" grep -q ghp_ main; then
  echo "✗ secret leaked into Git" >&2; exit 1
fi
echo "✓ no secrets in Git"

"$RECAP" apply "$H" --publish
"$RECAP" find "pods out of memory 502"
echo "✓ smoke test passed"
