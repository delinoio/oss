# thenv Devkit Mini App

This mini app provides metadata-only management for thenv bundles.

## Route Contract
- `/apps/thenv`

## Responsibilities
- List bundle version metadata and allow active version switching.
- View and update scope-level policy bindings.
- Browse audit events without exposing secret values.

## Security
- This UI must never render plaintext `.env` or `.dev.vars` payloads.
- All business operations are delegated to server-side Connect RPC adapters.
