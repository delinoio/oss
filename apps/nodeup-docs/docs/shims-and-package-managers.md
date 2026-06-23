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

- macOS and Linux: `$HOME/.local/bin`
- Windows: `$HOME\.local\bin`

Use `NODEUP_SHIM_DIR` or `--dir <path>` to choose another directory:

```bash
NODEUP_SHIM_DIR="$HOME/.local/bin" nodeup shim setup
nodeup shim setup --dir "$HOME/.local/bin"
```

The command is idempotent:

- Missing shims are created.
- Existing valid shims are reported as `existing`.
- Existing unrelated commands are reported as conflicts and are not replaced.
- Stale Nodeup symlinks are repaired.
- Ambiguous non-Nodeup files and different existing Windows executables are refused instead of overwritten.

If the shim directory is not active on `PATH`, human and JSON output include a `path_instruction` value for the current session. Add the shim directory to your shell profile or user PATH for future sessions.

Windows behavior differs because symlink creation may require privileges. Nodeup uses copied `.exe` aliases on Windows, so rerun `nodeup shim setup` after moving or updating the `nodeup.exe` binary.

## Windows Shim Alias Files

Windows shim aliases and runtime package-manager executables are separate files:

| Layer | Example | Meaning |
| --- | --- | --- |
| Nodeup shim alias | `npm.exe` | A copy or link of the Nodeup binary whose executable name lets Nodeup dispatch by `argv[0]`. |
| Delegated runtime executable | `bin/npm.cmd` | The package-manager command inside the selected Node.js runtime that Nodeup runs after resolution. |

The recommended first-party setup copies the Nodeup binary to `.exe` aliases such as `node.exe`, `npm.exe`, `npx.exe`, `yarn.exe`, and `pnpm.exe`. The alias file only controls how Windows starts Nodeup; it does not change which executable Nodeup checks inside the selected runtime. A batch file that calls `nodeup.exe` does not preserve the batch file name as Nodeup's `argv[0]`, so use copied or linked executable aliases for managed shim dispatch.

On Windows, command lookup depends on `PATH` order and `PATHEXT`. If another `npm.cmd` or `node.exe` appears earlier on `PATH`, Windows may run that command instead of the Nodeup shim. Check precedence with:

```powershell
where npm
where node
Get-Command npm -All
```

Place the Nodeup shim directory before other Node.js or package-manager directories when you want Nodeup-managed dispatch.

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

Invalid values identify the failed part and an exact correction example. For example, `pnpm@10.x` reports a version problem and suggests `pnpm@10.32.1`, `npm@10.0.0` reports an unsupported manager and points to exact `yarn@4.13.0` or `pnpm@10.32.1` values, and non-string JSON values report that `packageManager` must be a string shaped like `<manager>@<exact-semver>`.

Corepack descriptors are out of scope. Nodeup does not ask Corepack to interpret ranges, tags, or package-manager metadata because dispatch must stay deterministic across runtimes. Package-manager planning output reports `corepack_supported: false` in JSON or `corepack=unsupported` in human notices.

When Nodeup plans `yarn` or `pnpm`, human output and JSON output expose the requested command, mode, strategy, Corepack support state, nearest `package.json` path when known, planning reason, and selected executable. npm-exec mode also exposes the package spec and whether it is pinned.

## yarn Mapping

`yarn@1.x.y` maps to the classic Yarn package:

```bash
npm exec --yes --package yarn@1.22.22 -- yarn ...
```

`yarn@2+` maps to Yarn's CLI distribution package:

```bash
npm exec --yes --package @yarnpkg/cli-dist@4.13.0 -- yarn ...
```

When `packageManager` is absent and the runtime has `bin/yarn`, Nodeup runs it directly and labels the strategy as `direct-runtime-binary`. Otherwise it falls back to:

```bash
npm exec --yes --package @yarnpkg/cli-dist -- yarn ...
```

That fallback is unpinned and less reproducible because npm can resolve a different package version later. Nodeup reports it as `unpinned-npm-exec-fallback` and recommends adding an exact value such as `"packageManager": "yarn@4.13.0"` for reproducible projects.

## pnpm Mapping

Pinned pnpm maps to:

```bash
npm exec --yes --package pnpm@10.32.1 -- pnpm ...
```

When `packageManager` is absent and the runtime has `bin/pnpm`, Nodeup runs it directly and labels the strategy as `direct-runtime-binary`. Otherwise it falls back to:

```bash
npm exec --yes --package pnpm -- pnpm ...
```

That fallback is unpinned and less reproducible because npm can resolve a different package version later. Nodeup reports it as `unpinned-npm-exec-fallback` and recommends adding an exact value such as `"packageManager": "pnpm@10.32.1"` for reproducible projects.

## which Behavior

`nodeup which yarn` and `nodeup which pnpm` use the same planning rules as execution.

- Direct mode prints the runtime's `yarn` or `pnpm` executable and labels it as a direct runtime binary.
- npm-exec mode prints the runtime's `npm` executable and labels that `npm` will invoke the requested package-manager CLI.
- Missing direct commands include JSON diagnostics with checked paths, linked runtime names when applicable, install-on-demand eligibility, and PATH/PATHEXT guidance.

Direct-mode example:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/pnpm
nodeup: pnpm will run as direct runtime binary /home/me/.nodeup/data/toolchains/v22.1.0/bin/pnpm (strategy=direct-runtime-binary; package_json=/repo/package.json; reason=package-json-missing-field-direct; corepack=unsupported)
```

npm-exec-mode human example:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm
nodeup: pnpm will run via npm exec using package pnpm@10.32.1 (pinned; strategy=pinned-npm-exec; package_json=/repo/package.json; npm=/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm; reason=package-manager-pinned; corepack=unsupported)
```

npm-exec-mode JSON includes stable planning fields:

```json
{
  "runtime": "v22.1.0",
  "command": "pnpm",
  "requested_command": "pnpm",
  "executable_path": "/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm",
  "mode": "npm-exec",
  "reason": "package-manager-pinned",
  "package_manager_strategy": "pinned-npm-exec",
  "corepack_supported": false,
  "package_spec": "pnpm@10.32.1",
  "package_spec_pinned": true
}
```

Scripts that need the executable path should read `executable_path`. Automation that needs to understand package-manager dispatch should read `mode`, `package_manager_strategy`, `corepack_supported`, `package_spec`, and `package_spec_pinned`.

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
