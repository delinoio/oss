# Reference

## Stable Source Identifiers

- `github:owner/repo[@version]`
- `github:<host>/owner/repo[@version]`
- `gitlab:<host>/<namespace...>/<project>[@version]`

`@version` is an exact release tag request. Omit it to select the latest stable release. `@latest`, SemVer range-like selectors, channel selectors such as `@beta`, and numeric major-version pins such as `@1` are rejected with diagnostics.

## Target Model

binpm resolves release assets against the current host target:

- OS: `linux`, `darwin`, `windows`, `freebsd`
- CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
- Libc or ABI environment: `gnu`, `musl`, `msvc`, `any`

Unsupported operating systems or CPU architectures fail clearly instead of being mapped to a supported fallback target.

## Global Update Status

Local `binpm update [cmd...] [--local] [--dry-run]` is implemented for project tools. Global update is pending implementation: `binpm update --global` fails, including with `--dry-run`, and reports the supported workaround. Run `binpm outdated --global` to identify stale global tools, inspect each stale command with `binpm info --global <cmd>` for its recorded source and selected binary, then reinstall it with `binpm install <source> --as <cmd> --bin <selected_binary>`.
