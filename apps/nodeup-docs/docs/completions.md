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
- `self`
- `completions`

Subcommand scopes are not accepted. Unsupported scopes fail with `invalid-input`.

## Output Contract

Completion output is always raw script text on stdout:

```bash
RUST_LOG=off nodeup completions bash >nodeup.bash
RUST_LOG=off nodeup completions zsh >_nodeup
```

`--output json` and `--color always` do not wrap or style completion script output. `RUST_LOG=off` is the recommended redirection form because completion scripts are raw text, not structured command data.

## Logging

Completion generation logs include shell, command scope, and whether generation succeeded or failed. Logs are written to stderr when enabled. Use `RUST_LOG=off` when redirecting completion scripts so stdout contains only the generated script.
