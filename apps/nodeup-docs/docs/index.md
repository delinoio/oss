# Nodeup

Nodeup is a Rust-based Node.js version manager with predictable channel resolution, deterministic shell completions, and shim-based execution for `node`, `npm`, `npx`, `yarn`, and `pnpm`.

Use Nodeup when you want a single CLI that can install Node.js runtimes, select the active runtime by explicit command, directory override, or global default, and dispatch common Node.js executable names through managed shims.

## Start Here

- [Install and verify Nodeup](/installation).
- [Install a runtime and run a command](/getting-started).
- [Read the complete command reference](/commands).
- [Understand runtime resolution precedence](/runtime-resolution).
- [Use managed shims and package-manager dispatch](/shims-and-package-managers).
- [Integrate JSON output, colors, and logs](/output).

## Supported Hosts

Nodeup runtime installation and shim dispatch target:

- macOS x64 and arm64
- Linux x64 and arm64
- Windows x64 and arm64

x86 hosts are unsupported. If Nodeup detects an unsupported host, use an x64/arm64 machine or a supported CI image.

## Runtime Selectors

Nodeup accepts exact Node.js versions with or without the `v` prefix, reserved channels, and linked runtime names:

```bash
nodeup toolchain install 22.1.0
nodeup toolchain install v22.1.0
nodeup default lts
nodeup default current
nodeup default latest
nodeup toolchain link work-node /opt/node-v22
```

Reserved channel selectors are exact and lowercase: `lts`, `current`, and `latest`.
When exact-version selectors are tracked for update, Nodeup canonicalizes them to `v<semver>` and deduplicates semantically equivalent forms like `22.1.0` and `v22.1.0`. Exact-version selectors remain immutable pins during `nodeup update`.

## Validation Commands

Use these commands from the repository root when changing Nodeup documentation:

```bash
pnpm --filter nodeup-docs test
pnpm --filter nodeup-docs build
```
