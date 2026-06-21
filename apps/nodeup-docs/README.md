# nodeup-docs

Rspress-based documentation app for the `nodeup` project.

Production URL: https://nodeup.delino.io

## Commands

Run from repository root:

```bash
pnpm --filter nodeup-docs dev
pnpm --filter nodeup-docs test
pnpm --filter nodeup-docs build
pnpm --filter nodeup-docs preview
```

`pnpm --filter nodeup-docs dev` runs Rspress on fixed port `46250`.
`pnpm --filter nodeup-docs preview` serves the production build on fixed port `46251`.

## Files

- `rspress.config.ts`: Rspress site configuration and navigation.
- `docs/index.md`: Nodeup docs landing page.
- `docs/installation.md`: Installation, verification, supported hosts, storage roots, and mirror configuration.
- `docs/getting-started.md`: Installation and first-use guide.
- `docs/commands.md`: Full command reference and output shapes.
- `docs/runtime-resolution.md`: Selector forms, runtime resolution precedence, overrides, defaults, and release index cache.
- `docs/shims-and-package-managers.md`: Shim dispatch and `packageManager` handling for `yarn` and `pnpm`.
- `docs/output.md`: Human/JSON output, error envelopes, color precedence, and logging.
- `docs/completions.md`: Supported shells, command scopes, and raw completion output contract.
- `docs/releases.md`: Release artifacts, signing sidecars, direct-installer verification, and runtime archives.
- `docs/troubleshooting.md`: Common failure modes and validation commands.
- `docs/reference.md`: Stable CLI behavior and route map reference.
