# Completions

Nodeup generates shell completion scripts directly from the CLI definition.

## Supported Shells

```bash
nodeup completions bash
nodeup completions zsh
nodeup completions fish
nodeup completions powershell
nodeup completions elvish
```

Unsupported shell names fail with `invalid-input`.

## Command Scopes

Generate completions for all top-level commands:

```bash
nodeup completions bash
```

Limit generation to one top-level command:

```bash
nodeup completions bash toolchain
nodeup completions zsh override
```

Supported command scopes:

- `toolchain`
- `default`
- `show`
- `update`
- `check`
- `override`
- `which`
- `run`
- `shim`
- `self`
- `completions`

Subcommand scopes are not accepted. Use the parent top-level command instead:

```bash
nodeup completions bash toolchain
```

For example, `nodeup completions bash toolchain install` fails with `invalid-input` and points back to `nodeup completions bash toolchain`.

## Output Contract

Completion output is always raw script text on stdout:

```bash
RUST_LOG=off nodeup completions bash >nodeup.bash
RUST_LOG=off nodeup completions zsh >_nodeup
```

PowerShell:

```powershell
$env:RUST_LOG = "off"
nodeup completions powershell > nodeup.ps1
```

`--output json` and `--color always` do not wrap or style completion script output. Set `RUST_LOG=off` before redirecting because completion scripts are raw text, not structured command data.

## Install or Source Generated Scripts

Generating a completion script only writes the script text. Your shell must source it for the current session or load it from a completion directory for future sessions.

### Bash

For the current shell:

```bash
RUST_LOG=off nodeup completions bash >"$HOME/.nodeup.bash"
source "$HOME/.nodeup.bash"
```

For future shells, install the file in a directory loaded by your bash completion setup. Common user-level locations include `$XDG_DATA_HOME/bash-completion/completions/nodeup` or `$HOME/.local/share/bash-completion/completions/nodeup`:

```bash
install -d "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"
RUST_LOG=off nodeup completions bash >"${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/nodeup"
```

Some bash setups require adding a `source` line to `~/.bashrc` instead of using a completion directory.

### Zsh

Install the generated `_nodeup` file in a directory that appears in `fpath`, then start a new shell or reload completions:

```bash
install -d "$HOME/.zfunc"
RUST_LOG=off nodeup completions zsh >"$HOME/.zfunc/_nodeup"
```

Add this to `~/.zshrc` if `$HOME/.zfunc` is not already in `fpath`:

```zsh
fpath=("$HOME/.zfunc" $fpath)
autoload -Uz compinit
compinit
```

### Fish

Fish loads user completions from `~/.config/fish/completions`:

```fish
mkdir -p ~/.config/fish/completions
env RUST_LOG=off nodeup completions fish > ~/.config/fish/completions/nodeup.fish
```

Open a new fish session, or run `complete --erase nodeup` before testing a regenerated script.

### PowerShell

For the current PowerShell session:

```powershell
$env:RUST_LOG = "off"
nodeup completions powershell > "$HOME\nodeup-completions.ps1"
. "$HOME\nodeup-completions.ps1"
```

For future sessions, add the dot-source line to your PowerShell profile:

```powershell
if (-not (Test-Path -LiteralPath $PROFILE)) {
  New-Item -ItemType File -Force -Path $PROFILE | Out-Null
}
Add-Content -LiteralPath $PROFILE -Value '. "$HOME\nodeup-completions.ps1"'
```

Execution policy and profile location vary by Windows and PowerShell edition. Run `$PROFILE` to inspect the active profile path.

### Elvish

For the current shell:

```text
E:RUST_LOG=off nodeup completions elvish > ~/.nodeup-completions.elv
use ~/.nodeup-completions.elv
```

For future sessions, add the `use ~/.nodeup-completions.elv` line to your Elvish rc file, commonly `~/.elvish/rc.elv`.

## Logging

Completion generation logs include shell, command scope, and whether generation succeeded or failed. Logs are written to stderr when enabled. Use `RUST_LOG=off` when redirecting completion scripts so stdout contains only the generated script.
