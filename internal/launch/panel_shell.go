package launch

// panelCwdEncoder runs in /bin/sh, independent of the interactive shell. Encode
// every byte except '/' (including UTF-8 bytes), so paths cannot inject OSCs.
// od is POSIX and avoids depending on Python, Perl, or a particular locale.
const panelCwdEncoder = `printf '\033]7;file://localhost'
printf '%s' "$1" | od -An -v -t x1 | while read -r line; do
  for byte in $line; do
    case "$byte" in 2f) printf /;; *) printf '%%%s' "$byte";; esac
  done
done
printf '\007'
`

func panelCwdCommand() string {
	return "/bin/sh -c " + ShellEscape(panelCwdEncoder) + " sh \"$PWD\""
}

// panelPosixShellScript keeps all integration files private and ephemeral, on
// the machine running the shell (also for SSH/WSL). The supervising sh removes
// them on exit/HUP; no user startup file is written. Unsupported shells retain
// their ordinary login startup, without cwd reporting.
func panelPosixShellScript(shell string) string {
	return panelPosixStartupScript(shell, "")
}

// With a payload, start the agent at the first prompt, after environment hooks
// (such as mise's project activation) have run. Never feed commands through the
// terminal's stdin: it belongs to the agent.
func panelPosixStartupScript(shell, payload string) string {
	bashHook := `__agent_sessions_cwd() {
  local __as_result=$?
  ` + panelCwdCommand() + `
  return "$__as_result"
}
# A scalar PROMPT_COMMAND is one command, not an array to word-split.
if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )); then
  PROMPT_COMMAND=("${PROMPT_COMMAND[@]}" __agent_sessions_cwd)
else
  # Older bash only evaluates the first array element. Preserve every entry
  # as a separate command when converting to its supported scalar form.
  __as_prompt=
  for __as_command in "${PROMPT_COMMAND[@]}"; do
    __as_prompt="$__as_prompt$__as_command"$'\n'
  done
  unset PROMPT_COMMAND
  PROMPT_COMMAND="$__as_prompt"'__agent_sessions_cwd'
  unset __as_prompt __as_command
fi
`
	zshHook := `function __agent_sessions_cwd() {
  local __as_result=$?
  ` + panelCwdCommand() + `
  return $__as_result
}
typeset -ga precmd_functions
precmd_functions+=(__agent_sessions_cwd)
`
	fishHook := `function __agent_sessions_cwd --on-event fish_prompt
  ` + "/bin/sh -c " + ShellEscape(panelCwdEncoder) + ` sh "$PWD"
end`
	bashRC := ""
	fallback := `exec "$__as_shell" -l`
	if payload != "" {
		// The payload is POSIX sh, regardless of the user's shell syntax.
		run := JoinCommand(PosixBootstrapArgv(payload))
		bashHook += "__agent_sessions_run() {\n  " + run + "\n  exit $?\n}\n"
		bashHook += "if (( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )); then\n  PROMPT_COMMAND+=(__agent_sessions_run)\nelse\n  PROMPT_COMMAND=\"$PROMPT_COMMAND\"$'\\n'__agent_sessions_run\nfi\n"
		zshHook += "function __agent_sessions_run() {\n  " + run + "\n  exit $?\n}\nprecmd_functions+=(__agent_sessions_run)\n"
		// fish_prompt events run before the prompt function itself.
		fishHook += "\nfunction fish_prompt\n  " + run + "\n  exit $status\nend"
		// Login profiles commonly source .bashrc themselves. Track that using
		// a temporary DEBUG trap so activation isn't performed twice.
		bashRC = `__as_rc_loaded=
trap 'case "${BASH_SOURCE[0]}" in "$HOME/.bashrc") __as_rc_loaded=1;; esac' DEBUG
set -T
`
		fallback = `exec "$__as_shell" -ilc ` + ShellEscape("exec "+run)
	}
	bashAfterProfile := ""
	if payload != "" {
		bashAfterProfile = `set +T
trap - DEBUG
if [ -z "$__as_rc_loaded" ] && [ -r "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi
unset __as_rc_loaded
`
	}
	return `__as_shell=` + shell + `
case "${__as_shell##*/}" in
bash|zsh)
  __as_tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-sessions-shell.XXXXXXXX") || { ` + fallback + `; }
  trap 'rm -rf -- "$__as_tmp"' 0
  trap 'exit 129' HUP
  trap 'exit 143' TERM
  case "${__as_shell##*/}" in
  bash)
    cat > "$__as_tmp/bashrc" <<'AGENT_SESSIONS_BASH'
# --rcfile is ignored by login bash. Reproduce its login startup order here;
# the plain shell tab respects the user's profile's choice of rc files.
` + bashRC + `[ ! -r /etc/profile ] || . /etc/profile
for __as_profile in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
  if [ -r "$__as_profile" ]; then . "$__as_profile"; break; fi
done
unset __as_profile
` + bashAfterProfile + bashHook + `AGENT_SESSIONS_BASH
    "$__as_shell" --rcfile "$__as_tmp/bashrc" -i
    ;;
  zsh)
    # Each wrapper lets the user's startup file choose ZDOTDIR for the next
    # file. Restore it before the prompt so subsequent nested shells are normal.
    export __AS_USER_ZDOTDIR="${ZDOTDIR-$HOME}" __AS_WRAPPER="$__as_tmp"
    for __as_file in .zshenv .zprofile .zshrc .zlogin; do
      cat > "$__as_tmp/$__as_file" <<'AGENT_SESSIONS_ZSH'
ZDOTDIR=$__AS_USER_ZDOTDIR
AGENT_SESSIONS_ZSH
      printf '[ ! -r "$ZDOTDIR/%s" ] || source "$ZDOTDIR/%s"\n' "$__as_file" "$__as_file" >> "$__as_tmp/$__as_file"
      cat >> "$__as_tmp/$__as_file" <<'AGENT_SESSIONS_ZSH'
__AS_USER_ZDOTDIR=${ZDOTDIR-$HOME}
ZDOTDIR=$__AS_WRAPPER
AGENT_SESSIONS_ZSH
    done
    cat >> "$__as_tmp/.zlogin" <<'AGENT_SESSIONS_ZSH'
ZDOTDIR=$__AS_USER_ZDOTDIR
unset __AS_USER_ZDOTDIR __AS_WRAPPER
` + zshHook + `AGENT_SESSIONS_ZSH
    ZDOTDIR="$__as_tmp" "$__as_shell" -il
    ;;
  esac
  __as_status=$?
  exit "$__as_status"
  ;;
fish) exec "$__as_shell" -il -C ` + ShellEscape(fishHook) + ` ;;
*) ` + fallback + ` ;;
esac
`
}

// Profiles have already run when -Command executes. Keep their prompt as a
// scriptblock, rather than replacing its appearance or PSReadLine hooks.
const panelPowerShellHook = `$global:__AgentSessionsPrompt = $function:prompt
function global:prompt {
  $text = & $global:__AgentSessionsPrompt
  try {
    if ($PWD.Provider.Name -eq 'FileSystem') {
      $path = $PWD.ProviderPath.Replace('\', '/')
      if (-not $path.StartsWith('/')) { $path = '/' + $path }
      $encoded = ($path.Split('/') | ForEach-Object { [Uri]::EscapeDataString($_) }) -join '/'
      [Console]::Write(([char]27).ToString() + ']7;file://localhost' + $encoded + [char]7)
    }
  } catch {}
  $text
}`
