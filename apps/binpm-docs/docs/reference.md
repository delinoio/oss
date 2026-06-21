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
