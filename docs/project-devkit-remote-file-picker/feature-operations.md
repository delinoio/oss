# Feature: operations

## Storage
- Ephemeral client state for active request, picker selection, upload progress, and completion status.
- No standalone mini app database.
- Sensitive request tokens stay in memory and are never persisted.


## Security
- Validate signed URL origin/protocol and expiry before upload attempts.
- Validate signed URL host against declared provider before upload attempts.
- Enforce provider/method compatibility (`gcp-cloud-storage` only `PUT` in Phase 1).
- Validate callback return URLs with explicit protocol allowlist (`http`/`https`) before redirect fallback.
- Never log signed URL query secrets or provider access tokens.
- Enforce file type and size constraints before upload.


## Logging
Required baseline logs:
- Entry request validation result
- Picker source selection and source adapter failures
- Preprocessing decision (`skipped` in Phase 1)
- Upload request/result with request correlation id
- Return flow success/failure


## Build and Test
Current commands:
- `pnpm --filter devkit... test`
- Module-focused tests:
  - request parser validation
  - upload orchestrator success/failure
  - completion bridge channel fallback

