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

Invalid values identify the failed part and the smallest correction. For example, `pnpm@10.x` reports a version problem and suggests `pnpm@<major>.<minor>.<patch>`, `npm@10.0.0` reports an unsupported manager and points to `yarn` or `pnpm`, and non-string JSON values report that `packageManager` must be a string shaped like `<manager>@<exact-semver>`.

Corepack descriptors are out of scope. Nodeup does not ask Corepack to interpret ranges, tags, or package-manager metadata because dispatch must stay deterministic across runtimes. Nodeup uses the selected runtime's `npm exec`.

When Nodeup selects npm-exec mode, human output and JSON output expose the requested command, package spec, nearest `package.json` path when known, the runtime `npm` executable, the planning reason, and whether the package spec is pinned.

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

That fallback is unpinned. Nodeup reports it as an unpinned fallback and recommends adding an exact value such as `"packageManager": "yarn@4.13.0"` for reproducible projects.

## pnpm Mapping

Pinned pnpm maps to:

```bash
npm exec --yes --package pnpm@10.32.1 -- pnpm ...
```

When `packageManager` is absent and the runtime has `bin/pnpm`, Nodeup runs it directly. Otherwise it falls back to:

```bash
npm exec --yes --package pnpm -- pnpm ...
```

That fallback is unpinned. Nodeup reports it as an unpinned fallback and recommends adding an exact value such as `"packageManager": "pnpm@10.32.1"` for reproducible projects.

## which Behavior

`nodeup which yarn` and `nodeup which pnpm` use the same planning rules as execution.

- Direct mode prints the runtime's `yarn` or `pnpm` executable.
- npm-exec mode prints the runtime's `npm` executable and labels that `npm` will invoke the requested package-manager CLI.

Direct-mode example:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/pnpm
```

npm-exec-mode human example:

```text
/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm
nodeup: pnpm will run via npm exec using package pnpm@10.32.1 (pinned; package_json=/repo/package.json; npm=/home/me/.nodeup/data/toolchains/v22.1.0/bin/npm; reason=package-manager-pinned)
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
  "package_spec": "pnpm@10.32.1",
  "package_spec_pinned": true
}
```

Scripts that need the executable path should read `executable_path`. Automation that needs to understand package-manager dispatch should read `mode`, `package_spec`, and `package_spec_pinned`.

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
