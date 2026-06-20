# Reference

This page summarizes stable Nodeup contracts. For command syntax, see [Command Reference](/commands).

## Stable User-Facing Behavior

- Channel selectors are `lts`, `current`, and `latest`.
- Runtime selector precedence is explicit selector, directory override, then global default.
- Shim dispatch is deterministic by executable name for `node`, `npm`, `npx`, `yarn`, and `pnpm`.
- `package.json` `packageManager` support is strict for `yarn` and `pnpm`.
- Shell completions are deterministic for supported shells and top-level command scopes.
- Human output color precedence is `--color` > `NODEUP_COLOR` > `NO_COLOR` > stream-aware `auto`.
- JSON output never contains ANSI styling.

## Supported Hosts

Nodeup supports runtime installation and shim dispatch on:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

x86 hosts are unsupported.

## Route Map

- [Installation](/installation): installation methods, verification, supported hosts, storage roots, mirrors.
- [Getting Started](/getting-started): first runtime install, defaults, overrides, run, shims, JSON verification.
- [Command Reference](/commands): command-by-command behavior and output shapes.
- [Runtime Resolution](/runtime-resolution): selectors, precedence, overrides, defaults, release index cache.
- [Shims and Package Managers](/shims-and-package-managers): managed aliases and `packageManager` behavior.
- [Output](/output): human/JSON contracts, errors, color precedence, logs.
- [Completions](/completions): shells, command scopes, raw script output.
- [Releases](/releases): release artifacts, signing, direct-installer verification.
- [Troubleshooting](/troubleshooting): common errors and validation commands.
