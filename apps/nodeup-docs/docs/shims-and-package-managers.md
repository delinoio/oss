# Shims and Package Managers

Nodeup preserves rustup-like shim behavior: one binary can be invoked through managed executable names and dispatch based on `argv[0]`.

## Managed Aliases

Nodeup recognizes these executable names:

- `node`
- `npm`
- `npx`
- `yarn`
- `pnpm`

If the binary is linked or copied as one of those names, Nodeup:

1. Resolves the active runtime by directory override or global default.
2. Installs a missing version runtime selected by that active selector.
3. Plans the delegated command.
4. Runs the resolved executable with inherited stdio.

Normal management commands still use the `nodeup` executable name.

## Setup and Repair

Create or repair all managed aliases with one command:

```bash
nodeup shim setup
```

Default shim directory:

- macOS and Linux: `$HOME/.local/share/nodeup/shims`
- Windows: `$HOME\.local\share\nodeup\shims`

Use `NODEUP_SHIM_DIR` or `--dir <path>` to choose another directory:

```bash
NODEUP_SHIM_DIR="$HOME/.local/bin" nodeup shim setup
nodeup shim setup --dir "$HOME/.local/bin"
```

The command is idempotent:

- Missing shims are created.
- Existing valid shims are reported as `existing`.
- Stale symlinks or stale Windows copies are repaired.
- Ambiguous non-Nodeup files are refused instead of overwritten.

If the shim directory is not active on `PATH`, human and JSON output include a `path_instruction` value for the current session. Add the shim directory to your shell profile or user PATH for future sessions.

Windows behavior differs because symlink creation may require privileges. Nodeup uses copied `.exe` aliases on Windows, so rerun `nodeup shim setup` after moving or updating the `nodeup.exe` binary.

## Direct Dispatch

For `node`, `npm`, `npx`, and non-package-manager commands, Nodeup resolves the command under the selected runtime's `bin/` directory.

On Windows, primary executable names are normalized:

- `node` -> `node.exe`
- `npm`, `npx`, `yarn`, `pnpm`, `corepack` -> `<command>.cmd`
- other commands -> `<command>.exe`

## packageManager Discovery

For `yarn` and `pnpm`, Nodeup searches for the nearest `package.json` from the current working directory upward.

If `package.json` contains `packageManager`, it must be a string in this format:

```json
{
  "packageManager": "pnpm@10.32.1"
}
```

Strict rules:

- Supported managers are `yarn` and `pnpm`.
- Versions must be exact semantic versions.
- The requested command must match the configured manager.
- Unsupported managers and malformed values fail with `invalid-input`.
- Manager-command mismatches fail with `conflict`.

Corepack is out of scope. Nodeup uses the selected runtime's `npm exec`.

## yarn Mapping

`yarn@1.x.y` maps to the classic Yarn package:

```bash
npm exec --yes --package yarn@1.22.22 -- yarn ...
```

`yarn@2+` maps to Yarn's CLI distribution package:

```bash
npm exec --yes --package @yarnpkg/cli-dist@4.13.0 -- yarn ...
```

When `packageManager` is absent and the runtime has `bin/yarn`, Nodeup runs it directly. Otherwise it falls back to:

```bash
npm exec --yes --package @yarnpkg/cli-dist -- yarn ...
```

## pnpm Mapping

Pinned pnpm maps to:

```bash
npm exec --yes --package pnpm@10.32.1 -- pnpm ...
```

When `packageManager` is absent and the runtime has `bin/pnpm`, Nodeup runs it directly. Otherwise it falls back to:

```bash
npm exec --yes --package pnpm -- pnpm ...
```

## which Behavior

`nodeup which yarn` and `nodeup which pnpm` use the same planning rules as execution.

- Direct mode prints the runtime's `yarn` or `pnpm` executable.
- npm-exec mode prints the runtime's `npm` executable because `npm exec` will run the package-manager CLI.

## Failure Examples

This fails with `conflict`:

```json
{
  "packageManager": "pnpm@10.32.1"
}
```

```bash
yarn install
```

This fails with `invalid-input`:

```json
{
  "packageManager": "pnpm@10.x"
}
```
