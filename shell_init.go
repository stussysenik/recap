package main

import (
	"fmt"
	"os"
)

// cmdShellInit outputs shell hook code for the specified shell.
// The hook writes session metadata to ~/.recap/active/<PID>.json
// on each prompt, enabling CWD and last-command enrichment.
//
// Usage: eval "$(recap shell-init zsh)"
//        eval "$(recap shell-init bash)"
//        recap shell-init fish | source
func cmdShellInit() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: recap shell-init <zsh|bash|fish>\n")
		fmt.Fprintf(os.Stderr, "\nAdd to your shell config:\n")
		fmt.Fprintf(os.Stderr, "  zsh:  eval \"$(recap shell-init zsh)\"\n")
		fmt.Fprintf(os.Stderr, "  bash: eval \"$(recap shell-init bash)\"\n")
		fmt.Fprintf(os.Stderr, "  fish: recap shell-init fish | source\n")
		os.Exit(1)
	}

	shell := os.Args[2]
	switch shell {
	case "zsh":
		os.Stdout.WriteString(zshHook)
	case "bash":
		os.Stdout.WriteString(bashHook)
	case "fish":
		os.Stdout.WriteString(fishHook)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s (supported: zsh, bash, fish)\n", shell)
		os.Exit(1)
	}
}

// zshHook installs precmd and preexec hooks that write session state
// to the recap registry. Uses printf + mv for atomic writes.
const zshHook = `
# recap shell integration (zsh)
_recap_session_dir="$HOME/.recap/active"
mkdir -p "$_recap_session_dir" 2>/dev/null

_recap_last_cmd=""

_recap_preexec() {
    _recap_last_cmd="$1"
}

_recap_precmd() {
    local _pid=$$
    local _tty=$(tty 2>/dev/null | sed 's|/dev/||')
    local _shell="$SHELL"
    local _cwd="$PWD"
    local _now=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    local _tmpfile="$_recap_session_dir/${_pid}.json.tmp"
    local _file="$_recap_session_dir/${_pid}.json"

    # Escape strings for JSON
    _cwd=$(printf '%s' "$_cwd" | sed 's/\\/\\\\/g; s/"/\\"/g')
    _recap_last_cmd_escaped=$(printf '%s' "$_recap_last_cmd" | sed 's/\\/\\\\/g; s/"/\\"/g')

    printf '{"pid":%d,"tty":"%s","shell":"%s","cwd":"%s","last_cmd":"%s","updated_at":"%s"}' \
        "$_pid" "$_tty" "$_shell" "$_cwd" "$_recap_last_cmd_escaped" "$_now" > "$_tmpfile" && \
        mv "$_tmpfile" "$_file" 2>/dev/null
}

autoload -Uz add-zsh-hook
add-zsh-hook preexec _recap_preexec
add-zsh-hook precmd _recap_precmd
`

// bashHook uses PROMPT_COMMAND and trap DEBUG for the same effect.
const bashHook = `
# recap shell integration (bash)
_recap_session_dir="$HOME/.recap/active"
mkdir -p "$_recap_session_dir" 2>/dev/null

_recap_last_cmd=""

_recap_debug_trap() {
    _recap_last_cmd="$BASH_COMMAND"
}

_recap_prompt_command() {
    local _pid=$$
    local _tty=$(tty 2>/dev/null | sed 's|/dev/||')
    local _shell="$SHELL"
    local _cwd="$PWD"
    local _now=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    local _tmpfile="$_recap_session_dir/${_pid}.json.tmp"
    local _file="$_recap_session_dir/${_pid}.json"

    # Escape strings for JSON
    _cwd=$(printf '%s' "$_cwd" | sed 's/\\/\\\\/g; s/"/\\"/g')
    local _recap_last_cmd_escaped=$(printf '%s' "$_recap_last_cmd" | sed 's/\\/\\\\/g; s/"/\\"/g')

    printf '{"pid":%d,"tty":"%s","shell":"%s","cwd":"%s","last_cmd":"%s","updated_at":"%s"}' \
        "$_pid" "$_tty" "$_shell" "$_cwd" "$_recap_last_cmd_escaped" "$_now" > "$_tmpfile" && \
        mv "$_tmpfile" "$_file" 2>/dev/null
}

trap '_recap_debug_trap' DEBUG
PROMPT_COMMAND="_recap_prompt_command${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
`

// fishHook uses fish's event system for prompt and preexec.
const fishHook = `
# recap shell integration (fish)
set -g _recap_session_dir "$HOME/.recap/active"
mkdir -p "$_recap_session_dir" 2>/dev/null

set -g _recap_last_cmd ""

function _recap_preexec --on-event fish_preexec
    set -g _recap_last_cmd "$argv"
end

function _recap_prompt --on-event fish_prompt
    set -l _pid %self
    set -l _tty (tty 2>/dev/null | string replace '/dev/' '')
    set -l _shell "$SHELL"
    set -l _cwd "$PWD"
    set -l _now (date -u +"%Y-%m-%dT%H:%M:%SZ")
    set -l _tmpfile "$_recap_session_dir/$_pid.json.tmp"
    set -l _file "$_recap_session_dir/$_pid.json"

    # Escape strings for JSON
    set _cwd (string replace -a '\\' '\\\\' "$_cwd" | string replace -a '"' '\\"')
    set -l _cmd_escaped (string replace -a '\\' '\\\\' "$_recap_last_cmd" | string replace -a '"' '\\"')

    printf '{"pid":%d,"tty":"%s","shell":"%s","cwd":"%s","last_cmd":"%s","updated_at":"%s"}' \
        $_pid "$_tty" "$_shell" "$_cwd" "$_cmd_escaped" "$_now" > "$_tmpfile"
    and mv "$_tmpfile" "$_file" 2>/dev/null
end
`
