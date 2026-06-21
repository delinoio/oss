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
- Missing direct commands include JSON diagnostics with checked paths, linked runtime names when applicable, install-on-demand eligibility, and PATH/PATHEXT guidance.

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
