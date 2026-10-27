# recap shell integration for bash — add to ~/.bashrc:
#   eval "$(recap shell-init bash)"
recap() {
  case "$1" in
    start)
      RECAP_WRAPPED=1 command recap "$@" || return $?
      export RECAP_SESSION="$(command recap _active)" ;;
    join)
      RECAP_WRAPPED=1 command recap "$@" || return $?
      export RECAP_SESSION="${2:-$(command recap _active)}" ;;
    leave)
      RECAP_WRAPPED=1 command recap "$@" && unset RECAP_SESSION ;;
    stop)
      RECAP_WRAPPED=1 command recap "$@"; local rc=$?
      [ -z "$(command recap _active)" ] && unset RECAP_SESSION
      return $rc ;;
    *)
      RECAP_WRAPPED=1 command recap "$@" ;;
  esac
}

_recap_histline() { HISTTIMEFORMAT= builtin history 1 | sed 's/^ *[0-9]* *//'; }
_recap_last="$(_recap_histline)"
_recap_prompt() {
  local rc=$?
  local line; line="$(_recap_histline)"
  if [ -n "$RECAP_SESSION" ] && [ -n "$line" ] && [ "$line" != "$_recap_last" ]; then
    ( command recap _hook cmd --session "$RECAP_SESSION" --exit "$rc" --dir "$PWD" --tty "$(tty)" -- "$line" & )
  fi
  _recap_last="$line"
  return $rc
}
PROMPT_COMMAND="_recap_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}"

if [ -z "$RECAP_SESSION" ]; then
  _recap_active="$(command recap _active 2>/dev/null)"
  [ -n "$_recap_active" ] && printf '\033[33m●\033[0m recap %s recording — run \033[1mrecap join\033[0m to attach this tab\n' "$_recap_active"
  unset _recap_active
fi
