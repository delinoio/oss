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
nodeup --output json completions bash >nodeup.bash
nodeup --output json completions zsh >_nodeup
```

`--output json` and `--color always` do not wrap or style completion script output.

## Logging

Completion generation logs include shell, command scope, and whether generation succeeded or failed. Use `--output json` or `RUST_LOG=off` when redirecting completion scripts so stdout contains only the generated script.
