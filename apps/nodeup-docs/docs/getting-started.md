# Getting Started

## 1. Install Nodeup

Choose one installation method from [Installation](/installation), then verify the binary:

```bash
nodeup --version
nodeup show home
```

`nodeup show home` prints the data, cache, and config roots that Nodeup will use for runtimes, downloads, settings, and overrides.

## 2. Install a Node.js Runtime

Install a durable channel selector first:

```bash
nodeup toolchain install lts
```

The install command resolves channels through the Node.js release index, downloads the matching archive for the current host, verifies `SHASUMS256.txt`, and extracts the runtime into Nodeup's toolchains directory.

Check installed runtimes:

```bash
nodeup toolchain list
nodeup toolchain list --verbose
```

## 3. Set the Default Runtime

```bash
nodeup default lts
nodeup default
nodeup show active-runtime
```

`nodeup default <runtime>` installs exact or channel-selected runtimes when needed, records the selector, and tracks it for later `nodeup update` runs.

## 4. Run Commands

Run a command against a specific runtime:

```bash
nodeup run lts node --version
nodeup run --install lts npm --version
```

`nodeup run --install <runtime> ...` installs a missing version before executing the delegated command. Without `--install`, a missing version fails with a `not-found` error and a recovery hint.

## 5. Configure a Directory Override

Pin a project directory to a runtime:

```bash
cd ~/src/my-app
nodeup override set lts
nodeup show active-runtime
```

Set or remove an override for another directory:

```bash
nodeup override set lts --path ~/src/my-app
nodeup override unset --path ~/src/my-app
```

Runtime resolution for normal dispatch is explicit selector, then nearest directory override, then global default. See [Runtime Resolution](/runtime-resolution).

## Exact-Version Pins

Use exact versions when a project or CI job needs a fixed runtime:

```bash
nodeup toolchain install 22.1.0
nodeup default 22.1.0
nodeup run 22.1.0 node --version
nodeup override set 22.1.0 --path ~/src/my-app
```

Exact-version selectors may include or omit the `v` prefix. Nodeup stores tracked exact versions in canonical `v<semver>` form, treats `22.1.0` and `v22.1.0` as the same selector, and keeps exact versions immutable during `nodeup update`. Use exact versions when you need a fixed runtime. Use channels such as `lts` or `current` when you want `nodeup update` to move the selector as new releases become available.

To move a pin from one exact runtime to another, install or select the newer exact version explicitly:

```bash
nodeup toolchain install 22.2.0
nodeup default 22.2.0
nodeup override set 22.2.0 --path ~/src/my-app
```

## 6. Use Shims

When the same binary is linked or copied as `node`, `npm`, `npx`, `yarn`, or `pnpm`, Nodeup detects the executable name and dispatches to the active runtime:

```bash
nodeup shim setup
```

The command creates or repairs all managed aliases in the default shim directory:

- macOS and Linux: `$HOME/.local/bin`
- Windows: `$HOME\.local\bin`

If that directory is not already on `PATH`, human output includes the exact shell command for the current session. Add the same directory to your shell profile or user PATH for future sessions.

Use a custom shim directory when needed:

```bash
nodeup shim setup --dir "$HOME/.local/bin"
```

The Windows examples create `.exe` Nodeup shim aliases. Batch wrappers that call `nodeup.exe` do not preserve the wrapper name as Nodeup's `argv[0]`, so use copied or linked executable aliases for managed shim dispatch. The delegated package-manager files inside a Windows Node.js runtime are usually `bin/npm.cmd`, `bin/npx.cmd`, `bin/yarn.cmd`, and `bin/pnpm.cmd`. Keep the shim directory before other Node.js directories on `PATH`; use `where npm` or `Get-Command npm -All` if a different command is shadowing the shim.

```bash
node --version
npm --version
yarn install
pnpm test
```

Managed alias dispatch installs a missing version selected by the active selector before running the command.

## 7. Verify Automation Output

Use JSON mode for scripts:

```bash
nodeup --output json show active-runtime
nodeup --output json which node
```

Handled failures in JSON mode are written to stderr as:

```json
{
  "kind": "not-found",
  "message": "Runtime v22.1.0 is not installed. Hint: Install it with `nodeup toolchain install <runtime>` and retry `nodeup which ...`.",
  "exit_code": 5
}
```

JSON payloads never include ANSI styling.
