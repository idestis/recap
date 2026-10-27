# recap shell integration for fish — add to ~/.config/fish/config.fish:
#   recap shell-init fish | source
# `recap` is a function so join/leave can set RECAP_SESSION in this tab (SPEC D-06).
function recap --description 'recap: record incident context'
    switch "$argv[1]"
        case start
            RECAP_WRAPPED=1 command recap $argv; or return $status
            set -gx RECAP_SESSION (command recap _active)
        case join
            RECAP_WRAPPED=1 command recap $argv; or return $status
            if set -q argv[2]
                set -gx RECAP_SESSION $argv[2]
            else
                set -gx RECAP_SESSION (command recap _active)
            end
        case leave
            RECAP_WRAPPED=1 command recap $argv; and set -e RECAP_SESSION
        case stop
            RECAP_WRAPPED=1 command recap $argv
            set -l rc $status
            test -z "$(command recap _active)"; and set -e RECAP_SESSION
            return $rc
        case '*'
            RECAP_WRAPPED=1 command recap $argv
    end
end

function _recap_preexec --on-event fish_preexec
    set -g _recap_cmd "$argv[1]"
end

function _recap_postexec --on-event fish_postexec
    set -l rc $status
    set -l cmd "$_recap_cmd"
    set -g _recap_cmd ""
    test -n "$RECAP_SESSION"; or return 0
    test -n "$cmd"; or return 0
    string match -qr '^\s*recap(\s|$)' -- "$cmd"; and return 0
    command recap _hook cmd --session "$RECAP_SESSION" --exit $rc --dir "$PWD" --tty (tty 2>/dev/null) -- "$cmd" >/dev/null 2>&1 &
    disown 2>/dev/null
    return 0
end

# New tab while a session is recording: nudge, don't auto-join (SPEC D-06).
if test -z "$RECAP_SESSION"; and status is-interactive
    set -l _recap_active (command recap _active 2>/dev/null)
    if test -n "$_recap_active"
        printf '\033[33m●\033[0m recap %s recording — run \033[1mrecap join\033[0m to attach this tab\n' $_recap_active
    end
end
