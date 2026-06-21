# Reference

## Stable Routes

- `/`
- `/installation`
- `/getting-started`
- `/commands`
- `/local-tooling`
- `/cache-and-verification`
- `/troubleshooting`
- `/reference`

## Stable Source Identifiers

- `github:owner/repo[@version]`
- `github:<host>/owner/repo[@version]`
- `gitlab:<host>/<namespace...>/<project>[@version]`

## Target Model

The host target model is enum-driven:

- OS: `linux`, `darwin`, `windows`, `freebsd`
- CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
- Libc or ABI environment: `gnu`, `musl`, `msvc`, `any`

Current-host target detection must fail clearly for unsupported operating systems or CPU architectures instead of mapping them to a supported fallback target.

## Validation

```bash
pnpm --filter binpm-docs test
pnpm --filter binpm-docs build
```

The production output directory is `doc_build`.
