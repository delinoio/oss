# Reference

## Stable Source Identifiers

- `github:owner/repo[@version]`
- `github:<host>/owner/repo[@version]`
- `gitlab:<host>/<namespace...>/<project>[@version]`

## Target Model

binpm resolves release assets against the current host target:

- OS: `linux`, `darwin`, `windows`, `freebsd`
- CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
- Libc or ABI environment: `gnu`, `musl`, `msvc`, `any`

Unsupported operating systems or CPU architectures fail clearly instead of being mapped to a supported fallback target.
