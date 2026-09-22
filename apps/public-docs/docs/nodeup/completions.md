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

Generate a script scoped to one top-level command:

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

Subcommand scopes are not accepted and are not silently broadened. Use the parent top-level command instead:

```bash
nodeup completions bash toolchain
```

For example, `nodeup completions bash toolchain install` fails with `invalid-input` and points back to `nodeup completions bash toolchain`.

With `--output json`, invalid shell names and unsupported scopes still use JSON error envelopes on stderr. Successful completion scripts are the exception to JSON output mode: they remain raw script text on stdout.

## Output Contract

Completion output is always raw script text on stdout:

```bash
nodeup completions bash >nodeup.bash
nodeup completions zsh >_nodeup
```

PowerShell:

```powershell
nodeup completions powershell > nodeup.ps1
```

`--output json` and `--color always` do not wrap or style successful completion script output. Completion scripts are raw text, not structured command data. Errors still follow the selected output mode, so `nodeup --output json completions bad-shell` emits a JSON error envelope on stderr.

## Install or Source Generated Scripts

Generating a completion script only writes the script text. Your shell must source it for the current session or load it from a completion directory for future sessions.

### Bash

For the current shell:

```bash
nodeup completions bash >"$HOME/.nodeup.bash"
source "$HOME/.nodeup.bash"
```

For future shells, install the file in a directory loaded by your bash completion setup. Common user-level locations include `$XDG_DATA_HOME/bash-completion/completions/nodeup` or `$HOME/.local/share/bash-completion/completions/nodeup`:

```bash
install -d "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"
nodeup completions bash >"${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/nodeup"
```

Some bash setups require adding a `source` line to `~/.bashrc` instead of using a completion directory.

### Zsh

Install the generated `_nodeup` file in a directory that appears in `fpath`, then start a new shell or reload completions:

```bash
install -d "$HOME/.zfunc"
nodeup completions zsh >"$HOME/.zfunc/_nodeup"
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
nodeup completions fish > ~/.config/fish/completions/nodeup.fish
```

Open a new fish session, or run `complete --erase nodeup` before testing a regenerated script.

### PowerShell

For the current PowerShell session:

```powershell
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
nodeup completions elvish > ~/.nodeup-completions.elv
use ~/.nodeup-completions.elv
```

For future sessions, add the `use ~/.nodeup-completions.elv` line to your Elvish rc file, commonly `~/.elvish/rc.elv`.

## Logging

Completion generation logs include shell, command scope, and whether generation succeeded or failed. Nodeup logging defaults off for completion generation, so redirected completion files contain only the generated script. Set `RUST_LOG=nodeup=debug` or another explicit filter to enable tracing on stderr. Use `RUST_LOG=off` only when a wrapper also needs stderr to stay quiet after setting a logging filter elsewhere.
