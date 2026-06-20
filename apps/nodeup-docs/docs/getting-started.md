# Getting Started

## 1. Install Nodeup

Choose one installation method from [Installation](/installation), then verify the binary:

```bash
nodeup --version
nodeup show home
```

`nodeup show home` prints the data, cache, and config roots that Nodeup will use for runtimes, downloads, settings, and overrides.

## 2. Install a Node.js Runtime

Install one or more exact versions or channels:

```bash
nodeup toolchain install 22.1.0
nodeup toolchain install lts current
```

The install command resolves channels through the Node.js release index, downloads the matching archive for the current host, verifies `SHASUMS256.txt`, and extracts the runtime into Nodeup's toolchains directory.

Check installed runtimes:

```bash
nodeup toolchain list
nodeup toolchain list --verbose
```

## 3. Set the Default Runtime

```bash
nodeup default 22.1.0
nodeup default
nodeup show active-runtime
```

`nodeup default <runtime>` installs exact or channel-selected runtimes when needed, records the selector, and tracks it for later `nodeup update` runs.

## 4. Run Commands

Run a command against a specific runtime:

```bash
nodeup run 22.1.0 node --version
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
nodeup override set 22.1.0 --path ~/src/my-app
nodeup override unset --path ~/src/my-app
```

Runtime resolution for normal dispatch is explicit selector, then nearest directory override, then global default. See [Runtime Resolution](/runtime-resolution).

## 6. Use Shims

When the same binary is linked or copied as `node`, `npm`, `npx`, `yarn`, or `pnpm`, Nodeup detects the executable name and dispatches to the active runtime:

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
  "exit_code": 1
}
```

JSON payloads never include ANSI styling.
