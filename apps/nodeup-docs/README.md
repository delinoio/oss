# nodeup-docs

Rspress-based static documentation app for the `nodeup` project.

## Commands

Run from repository root:

```bash
pnpm --filter nodeup-docs dev
pnpm --filter nodeup-docs build
pnpm --filter nodeup-docs test
```

`pnpm --filter nodeup-docs dev` runs Rspress on fixed port `46250`.
`pnpm --filter nodeup-docs test` performs the production static-site build used for local and CI validation.
Rspress writes the static output to `doc_build`.

## Files

- `rspress.config.ts`: Rspress site configuration and navigation.
- `docs/index.md`: Site landing page.
- `docs/guide/getting-started.md`: Initial user-facing setup guide.
