# Feature: operations

## Storage
- Source pages and configuration are stored in git at `apps/public-docs`.
- No runtime database or server-side persistent state is used by this project.


## Security
- Do not include secrets, tokens, or internal-only credentials in docs content.
- Keep user-facing guidance consistent with approved repository policies.
- Link only to trusted and intended public destinations.


## Logging
Required baseline logs and checks:
- Mintlify broken-link check output for pull request validation.
- CI job results for `node-public-docs-test`.
- Pull request review notes for documentation lifecycle policy changes.


## Build and Test
Current commands:
- Dev preview: `pnpm --filter public-docs dev`
- Link validation test: `pnpm --filter public-docs test`
- Dependency installation: `pnpm install`

