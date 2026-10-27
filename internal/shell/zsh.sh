# recap shell integration for zsh — add to ~/.zshrc:
#   eval "$(recap shell-init zsh)"
# `recap` is a function so join/leave can set RECAP_SESSION in this tab (SPEC D-06).
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

_recap_preexec() { _recap_cmd="$1"; }
_recap_precmd() {
  local rc=$?
  if [ -n "$RECAP_SESSION" ] && [ -n "$_recap_cmd" ]; then
    command recap _hook cmd --session "$RECAP_SESSION" --exit "$rc" --dir "$PWD" --tty "$TTY" -- "$_recap_cmd" &!
  fi
  _recap_cmd=""
}
autoload -Uz add-zsh-hook
add-zsh-hook preexec _recap_preexec
add-zsh-hook precmd _recap_precmd

# New tab while a session is recording: nudge, don't auto-join (SPEC D-06).
if [ -z "$RECAP_SESSION" ]; then
  _recap_active="$(command recap _active 2>/dev/null)"
  [ -n "$_recap_active" ] && print -P "%F{yellow}●%f recap ${_recap_active} recording — run %Brecap join%b to attach this tab"
  unset _recap_active
fi
