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

## Production Deployment

- Canonical production URL: `https://binpm.delino.io`
- Deployment target: Cloudflare Pages static site output from `doc_build`

The production URL is deployment metadata. Documentation content must be sourced from repository contracts and must not infer behavior or page contents from the live site.

## Stable Source Identifiers

- `github:owner/repo[@version]`
- `github:<host>/owner/repo[@version]`
- `gitlab:<host>/<namespace...>/<project>[@version]`

`@version` is an exact release tag request. Omit it to select the latest stable release. `@latest`, SemVer range-like selectors, channel selectors such as `@beta`, and numeric major-version pins such as `@1` are rejected with diagnostics.

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
