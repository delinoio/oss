# Shims and shells

> CowCache 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Install and inspect

Shims are optional. Explicit `cowc run` remains available without them. Install the real tools first, then install CowCache's `cargo`, `rspack`, and `rsbuild` shims:

```sh
cowc shim install
cowc shim list
cowc shim doctor
```

The shims belong in a current-user-private directory. Installation must not overwrite original tools. Reinstalling CowCache-owned shims is idempotent. A conflicting file owned by another tool must cause an actionable error; do not remove that file without identifying its owner.

## Activate the current session

`cowc shell init` prints activation code. Diagnostics must remain separate from that code. After reviewing the generated code, apply it in the matching shell.

Bash:

```bash
eval "$(cowc shell init bash)"
```

Zsh:

```zsh
eval "$(cowc shell init zsh)"
```

For PowerShell 7, invoke `cowc shell init <shell>` with the PowerShell identifier specified by the released CLI. Review its output, then evaluate the activation code in PowerShell 7. The exact identifier remains pending runtime integration; this guide does not invent one. Fish and cmd.exe-specific activation are outside the first release.

After activation, run `cowc shim doctor` to inspect PATH priority and the selected real executables. Shell aliases or functions can still take priority over PATH shims. Session activation alone does not configure future shells.

## Persistent setup

For Bash and Zsh, the intended commands are:

```sh
cowc shell setup bash
cowc shell setup zsh
```

For PowerShell 7, use the released CLI's PowerShell shell identifier with `cowc shell setup`. The identifier remains subject to runtime integration confirmation.

Setup must show the target file and proposed change and request confirmation. It adds only a CowCache-owned block and preserves unrelated content. Repeated setup is idempotent. If automatic file selection is ambiguous, choose the intended shell configuration file with `--file`.

For example, after choosing a Bash configuration file yourself:

```sh
cowc shell setup bash --file "./chosen-shell-config" --yes
```

The path is a placeholder, not a recommendation for a startup filename. Noninteractive setup requires `--yes`; it authorizes the displayed class of shell-file update, not cache replacement or deletion. Failed writes must preserve the original file.

Persistent setup applies to new shells. For the current session, use the matching `shell init` command above, or open a new shell. Confirm PATH order with `cowc shim doctor`.

## Tool selection

Each invocation selects the first real executable in PATH after excluding CowCache shims. Installation does not pin a tool path. Changing PATH or a version-manager selection must affect the next invocation. CowCache must prevent recursion while preserving rustup toolchain selection, including explicit toolchain arguments.

A shim preserves the tool's arguments, working directory, stdout, stderr, and exit code. Tool arguments must not become CowCache options. Put CowCache options before `--` in explicit `cowc run`, or use saved configuration. If no real executable exists, diagnostics must explain how to restore one and return command-not-found status 127.

## Managed and pass-through commands

Recognized cache-using operations, such as builds, checks, tests, and development servers, enter the same managed lifecycle as `cowc run`.

Version/help requests and cache-nonmutating queries pass through without preparation, prompts, or cache locks. Calls outside Git also pass through; that does not provide managed-cache support outside Git. Unknown commands and custom Cargo subcommands pass through without automatic protection. Use explicit `cowc run` if such a command needs cache management and its preset can be verified.

Shim-dispatched `cargo clean` waits for the relevant lock without preparing or cloning a cache. It preserves Cargo's original arguments and deletion behavior. `cowc clean` instead refuses an in-use cache immediately.

Same-session/same-cache nesting reuses protection without preparation. Different-cache nesting remains managed. See [process ownership](/cowcache/concurrency) before running nested tools or watch servers.

## Bypass and interception limits

Set `COWC_BYPASS=1` to pass through to the real tool without cache management. In Bash or Zsh, a one-command example is:

```sh
COWC_BYPASS=1 cargo build
```

In PowerShell 7, set `$env:COWC_BYPASS` to `"1"` for the intended invocation, then restore its previous value or remove it if it was previously unset. Do not leave bypass enabled unintentionally. Never run a bypassed cache writer concurrently with managed access to the same cache.

PATH shims cannot guarantee interception of shell aliases/functions, absolute executable paths, package-manager-local binaries, npm/pnpm/yarn scripts, or JavaScript API calls. First-release automatic script and `node_modules/.bin` integration is excluded. Use explicit `cowc run` and the [Rspack/Rsbuild configuration integration](/cowcache/configuration#rspack-and-rsbuild-configuration) where applicable; wrapping alone does not resolve unknown cache configuration.

## Remove activation and shims

Remove persistent registration before uninstalling the CLI:

```sh
cowc shell remove bash
cowc shell remove zsh
```

Use only the shell you configured. For PowerShell 7, use its released shell identifier with `cowc shell remove`. If setup used `--file`, select that same file for removal. Removal shows the target and change, requires interactive confirmation or noninteractive `--yes`, removes only the owned block, and preserves unrelated text. Repeated removal is idempotent; failed writes preserve the original file.

Removal affects new shells. In the current session, remove the CowCache shim directory from PATH and refresh shell command lookup, or open a new shell after persistent removal. Do not erase unrelated PATH entries.

```sh
cowc shim uninstall
```

Shim uninstall removes only CowCache-owned shims. It preserves caches, settings, and shell registration blocks. If blocks remain, use `cowc shell remove` before removing the CLI. Recheck tool selection in a new shell.

## Recovery

If an unexpected tool runs, inspect `cowc shim doctor`, PATH order, shell aliases/functions, and version-manager selection. Refresh the shell's cached command lookup or open a new shell after changing PATH. If a real tool is missing, restore its installation or PATH entry; repeatedly reinstalling shims does not install the tool.

If setup/removal fails, retain the original configuration file, correct its permissions or choose an unambiguous file with `--file`, and retry. Do not replace the whole startup file. If uninstall reports a foreign file, identify it before acting; ownership checks must preserve unrelated files.
