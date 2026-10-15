// SPDX-License-Identifier: Apache-2.0
package terminal

// The scripts below are written to a private per-terminal directory and load
// only after the account's own shell initialization. They shorten the
// directory segment of the prompt while the shell's physical working directory
// is the original General Chat directory. They never touch user files, PWD or
// command output, and a prompt whose directory segment is not recognized stays
// unchanged. Fish reads its private handoff file after account configuration;
// the other scripts remove the inherited variable so child commands do not
// receive it.

// zshEnvScript runs first (ZDOTDIR points at the wrapper directory). It runs
// the account's .zshenv with the account's ZDOTDIR semantics, remembers where
// the account's remaining startup files live, then re-points ZDOTDIR here so
// zsh reads the wrapper .zshrc.
const zshEnvScript = `# DeliDev process-local prompt integration (generated; safe to delete).
typeset -g __delidev_wrapper_dir=$ZDOTDIR
typeset -g __delidev_chat_dir=$DELIDEV_TERMINAL_CHAT_DIRECTORY
unset DELIDEV_TERMINAL_CHAT_DIRECTORY
unset ZDOTDIR
if [[ -r $HOME/.zshenv ]]; then
  source $HOME/.zshenv
fi
typeset -g __delidev_user_zdotdir=${ZDOTDIR:-$HOME}
typeset -g __delidev_zdotdir_was_set=0
if [[ -n ${ZDOTDIR+x} ]]; then
  __delidev_zdotdir_was_set=1
fi
ZDOTDIR=$__delidev_wrapper_dir
`

const zshRCScript = `# DeliDev process-local prompt integration (generated; safe to delete).
ZDOTDIR=$__delidev_user_zdotdir
if [[ -r $ZDOTDIR/.zshrc ]]; then
  source $ZDOTDIR/.zshrc
fi
if [[ $__delidev_zdotdir_was_set == 0 && $ZDOTDIR == $__delidev_user_zdotdir ]]; then
  unset ZDOTDIR
fi
unset __delidev_wrapper_dir __delidev_user_zdotdir __delidev_zdotdir_was_set

typeset -g __delidev_native_prompt= __delidev_short_prompt=

__delidev_precmd() {
  emulate -L zsh
  setopt extended_glob
  # Restore the native prompt if the previous prompt was ours and nothing
  # (for example a theme) has replaced it since.
  if [[ -n $__delidev_short_prompt && $PROMPT == "$__delidev_short_prompt" ]]; then
    PROMPT=$__delidev_native_prompt
  fi
  __delidev_native_prompt= __delidev_short_prompt=
  [[ ${PWD:P} == "$__delidev_chat_dir" ]] || return 0
  local sentinel=$'\x01' pattern='%[-0-9]#[~/dcC.]' candidate=$PROMPT
  candidate=${candidate//'%%'/$sentinel}
  candidate=${candidate//${~pattern}/chat}
  candidate=${candidate//$sentinel/'%%'}
  if [[ $candidate != "$PROMPT" ]]; then
    __delidev_native_prompt=$PROMPT
    __delidev_short_prompt=$candidate
    PROMPT=$candidate
  fi
  return 0
}
precmd_functions+=(__delidev_precmd)
`

const bashRCScript = `# DeliDev process-local prompt integration (generated; safe to delete).
__delidev_chat_dir=$DELIDEV_TERMINAL_CHAT_DIRECTORY
unset DELIDEV_TERMINAL_CHAT_DIRECTORY
if [ -r "$HOME/.bashrc" ]; then
  . "$HOME/.bashrc"
fi

__delidev_native_ps1=
__delidev_short_ps1=

__delidev_shorten_ps1() {
  local ps1 out c next i n
  if [ -n "$__delidev_short_ps1" ] && [ "$PS1" = "$__delidev_short_ps1" ]; then
    PS1=$__delidev_native_ps1
  fi
  __delidev_native_ps1=
  __delidev_short_ps1=
  case $PS1 in
    *\\w* | *\\W*) ;;
    *) return 0 ;;
  esac
  [ "$(builtin pwd -P)" = "$__delidev_chat_dir" ] || return 0
  ps1=$PS1
  out=
  i=0
  n=${#ps1}
  while [ "$i" -lt "$n" ]; do
    c=${ps1:$i:1}
    if [ "$c" = '\' ] && [ $((i + 1)) -lt "$n" ]; then
      next=${ps1:$((i + 1)):1}
      case $next in
        w | W) out="${out}chat" ;;
        *) out="${out}${c}${next}" ;;
      esac
      i=$((i + 2))
    else
      out="${out}${c}"
      i=$((i + 1))
    fi
  done
  if [ "$out" != "$PS1" ]; then
    __delidev_native_ps1=$PS1
    __delidev_short_ps1=$out
    PS1=$out
  fi
  return 0
}

# Keep the status of the user's last command visible to the prompt.
__delidev_prompt_command() {
  local __delidev_status=$?
  __delidev_shorten_ps1
  return $__delidev_status
}

case "$(declare -p PROMPT_COMMAND 2>/dev/null)" in
  "declare -a"*) PROMPT_COMMAND+=(__delidev_prompt_command) ;;
  *) PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND
}__delidev_prompt_command" ;;
esac
`

const fishInitScript = `# DeliDev process-local prompt integration (generated; safe to delete).
set -e DELIDEV_TERMINAL_CHAT_DIRECTORY
set -l __delidev_handoff_dir (status dirname)
if read --global --unexport --line __delidev_chat_dir < "$__delidev_handoff_dir/chat-directory"
    if functions -q prompt_pwd
        functions -c prompt_pwd __delidev_native_prompt_pwd
        function prompt_pwd --description 'prompt_pwd with the DeliDev chat label'
            set -l here (builtin pwd -P)
            if test "$here" = "$__delidev_chat_dir"
                echo chat
            else
                __delidev_native_prompt_pwd $argv
            end
        end
    end
end
`

const powerShellInitScript = `# DeliDev process-local prompt integration (generated; safe to delete).
$script:DeliDevChatDirectory = $env:DELIDEV_TERMINAL_CHAT_DIRECTORY
Remove-Item Env:DELIDEV_TERMINAL_CHAT_DIRECTORY -ErrorAction SilentlyContinue
if (Test-Path Function:\prompt) {
    $global:DeliDevNativePrompt = (Get-Item Function:\prompt).ScriptBlock
    $global:DeliDevChatDirectory = $script:DeliDevChatDirectory
    function global:prompt {
        $text = & $global:DeliDevNativePrompt
        try {
            $location = $executionContext.SessionState.Path.CurrentLocation
            if ($location.Provider.Name -eq 'FileSystem') {
                $comparison = [System.StringComparison]::Ordinal
                if ($env:OS -eq 'Windows_NT') { $comparison = [System.StringComparison]::OrdinalIgnoreCase }
                $physical = $location.ProviderPath
                if ([string]::Equals($physical, $global:DeliDevChatDirectory, $comparison) -and $text -is [string]) {
                    $text = $text.Replace($location.Path, 'chat')
                }
            }
        } catch { }
        return $text
    }
}
`
