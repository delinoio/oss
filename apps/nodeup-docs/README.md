# nodeup-docs

Rspress-based documentation app for the `nodeup` project.

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
- `docs/getting-started.md`: Installation and first-use guide.
- `docs/reference.md`: CLI behavior and contract reference.
