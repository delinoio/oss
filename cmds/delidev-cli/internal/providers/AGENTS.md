# DeliDev provider inspection ownership

- Follow `docs/cmds-delidev-providers-contract.md`, the activation/catalog contracts and issue #1148. Go owns the canonical 35-entry ordered registry, fixed non-inference inspection targets and protected credential use. Endpoint/protocol/authentication must match the closed official profile; copied names, preset IDs, response URLs and public catalogs cannot grant authentication authority.
- Bound private verification and complete discovery together to the original 20-second/32-page/10,000-entry/4-MiB-per-response/16-MiB-total/32-KiB-header profile. Count filtered identities, reject duplicate identities/cursor cycles and publish no partial catalog. Discard private identity, financial, invocation URL and organization fields; keys never enter URLs or diagnostics.
- `cmd/guidance` generates `apps/delidev/src-tauri/provider-guidance.generated.json` from reconciled static official documentation/key-creation metadata. Regenerate with `go -C cmds/delidev-cli run ./internal/providers/cmd/guidance > apps/delidev/src-tauri/provider-guidance.generated.json` from the repository root. Keep the freshness test and native compiled selector aligned; guidance grants no account, discovery or execution capability.

- Keep generated native guidance byte-identical across hosts through its exact-path LF attribute; regenerate from the registry and preserve the freshness check rather than accepting platform-specific JSON bytes.

- oauth_clients.json contains only compiled DeliDev public registration/ordinary API acceptance metadata, shared with native. Pending/unverified profiles cannot advertise support. Never copy other applications client IDs or accept user-selected OAuth authorities. Follow `docs/cmds-delidev-account-oauth-contract.md`.
