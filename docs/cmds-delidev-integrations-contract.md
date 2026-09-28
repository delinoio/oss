# DeliDev GitHub Integration Profiles

## Scope

`cmds/delidev-cli/internal/integrations/github`, the domain integration model, server IntegrationService handlers and CLI integration commands own named server-side GitHub.com PAT profiles. This increment implements profile metadata, direct native credential generations and authenticated identity inspection. Repository feature authorization, issue/PR queries, rulesets/reviewer checks, creation-form verification and remediation remain separate required issue #964 work; identity success cannot stand in for them.

## Runtime and Language

Go owns authorization, SQLite metadata, native credential coordination and bounded GitHub HTTPS. React uses generated Connect Query descriptors; the desktop host does not implement GitHub business logic.

## Users and Operators

The server owner and paired product clients manage profiles. Worker credentials cannot invoke these operations or receive PAT bytes. The selected server computer owns the protected OS credential entry, even when a remote desktop or CLI submits the write-only token.

## Interfaces and Contracts

`IntegrationService` provides `SaveIntegrationProfile`, `ReplaceIntegrationToken`, `ValidateIntegrationProfile` and `DeleteIntegrationProfile`. Generic resource reads/list/snapshots/events expose `integration` metadata. Save accepts strict schema-version-1 definition JSON containing `name`, provider `github.com`, `token_kind` (`fine-grained` preferred or `classic`) and optional `resource_owner`; fine-grained profiles require an owner. The token type and resource owner are explicit user declarations, fixed for the profile lifetime. Renaming cannot alter connection state. A different owner/type requires another profile.

CLI commands are `integration create --input FILE|-`, `integration edit --id ID --revision N --input FILE|-`, `integration replace-token --id ID --revision N --pat-stdin`, `integration validate|delete --id ID --revision N`, and ordinary `integration list|get|snapshot`. PAT input is bounded to 512 visible ASCII bytes, permits one conventional terminal LF/CRLF and never appears in argv. The PAT and server-authentication token cannot share stdin. Accepted operations with cleanup/inspection problems return current metadata plus a typed failure and the original request ID; the CLI preserves that result with a nonzero status.

Every mutation binds the current authenticated principal, original UUID-v7 request ID, profile identity and expected revision. Creation allocates its own UUID-v7. Receipts contain references rather than token or historical profile bodies. Exact retries return current metadata; changed input under a used request ID conflicts. Deletion tombstones prevent old saves/replacements from recreating profiles.

Replacement first commits denial of the old connection and a pending original request. Native storage writes the new request-bound generation and removes other owned generations. Completion rechecks the pending request and commits the new connection. Identity inspection then uses a separately retained validation request identity. Already completed replacement replay neither rewrites native storage nor revalidates a newer generation. Pending operations expose their original expected revision as a decimal string so desktop retries cannot round it. If the original replacement token is lost, explicit profile deletion may abandon replacement and reconcile all its staged references.

Deletion first removes active connection authority, cancels and joins that profile's active HTTP/token lifetime, and durably records pending cleanup. It deletes all owned native generation references before final profile deletion. Failed or interrupted cleanup stays pending and can be resumed using the original mutation. Existing repository `integration_id` associations remain unchanged and require explicit reconfiguration; no fallback profile, system token or Worker Git credential is selected.

Validation reads exactly the current native generation, then releases the integration gate and SQLite transaction before HTTPS. At most one inspection per profile and eight server-wide inspections are active. Publication rechecks principal authorization, original revision and connection generation. Replacement/deletion cancels and joins the original inspection before completing the native change; original cleanup cannot remove a newer check. Accepted validation replay is read-only. A rename during inspection may cause a revision conflict rather than overwrite newer metadata.

The closed client sends only authenticated `GET https://api.github.com/user`, with API version `2026-03-10`, a 15-second total timeout and bounded transport/header/body limits. It disables ambient proxies, cookies, redirects and automatic request retries. Duplicate/invalid JSON, non-user responses, malformed stable identities and oversized responses remain unavailable. It retains only canonical decimal numeric ID, node ID and current login. GitHub's `/user` check requires no additional fine-grained permission; it does not request email or private-user data.

States distinguish `identity-verified`, `invalid-token`, `access-restricted`, `sso-required`, `rate-limited` and `unavailable`. Known SSO headers classify SSO without retaining or following their private URL. A generic 403 cannot establish the precise organization-approval/SSO/permission cause. Failure replaces earlier identity proof for that generation; neither success nor failure proves individual repository/PR/issue/check/ruleset/reviewer capability.

## Storage

Only non-secret definitions, generation references, current typed validation, pending operations and receipts enter server SQLite. Direct PAT bytes use the separate native service in the credential contract. Private generation metadata and keyed receipt commitments are outside/inside SQLite respectively, but neither contains raw tokens, encrypted PAT payloads, unkeyed token hashes or owner-key bytes. The protected server owner identity keys domain-separated request commitments and must accompany restoration of its matching scope.

Settings > Integrations > GitHub lists, creates, renames, connects, validates and deletes profiles. Repository settings explicitly select a profile. The desktop accepts PATs as transient password input, clears the input on send/inactivation, clears its byte buffer after either outcome and drops mutation cache state. An uncertain retry retains only mutation identity and requires token reentry. Read caches, configuration, synchronized settings and browser storage never contain saved PATs. JavaScript/transport-owned transient copies are not claimed to be cryptographically erased.

## Security

Owner/client authorization is checked at the public boundary and transactionally on reads, receipt replay and mutations. Revocation cancels active client requests. Native storage and identity inspection are independent of AI accounts; no PAT is offered to Worker/harness subprocesses or native Git. Generic configuration writes cannot forge integration connection, validation or pending state. All selected-provider/network error bodies are discarded, and no new arbitrary authority or HTTP proxy is exposed.

## Logging

Structured logs include bounded operation/state, opaque profile/request identifiers, typed error codes, pending/deleted/replayed flags and correlation identity. No PAT, identity response body, user email, profile document or GitHub diagnostic body is logged. Native storage uses the separate credential logging boundary.

## Build and Test

Run focused race checks for the domain, GitHub adapter and server/CLI profile lifecycle; run the complete DeliDev Go tests and vet. Regenerate/lint protobuf and prove reproducible generated files. Frontend changes require `pnpm --dir apps/delidev test`; generated app/client `dist` output must be removed afterward.

Tests use real temporary SQLite and authenticated loopback Connect, injected private credential faults and scripted HTTP observations. They cover generation replay, independent profiles, denial before cleanup, lost native acknowledgment, canceled inspection, secret-free server files, exact decimal revisions and transient frontend input/cache behavior. Real Go-server UI/CLI tests cover non-secret profile creation/rename. These fixtures are not real GitHub PAT/organization/creation-form acceptance; record native and real-account evidence separately in the ledger.

## Dependencies and Integrations

The credential package owns native storage and immutable generation markers. Existing ResourceService provides metadata reads/events. Generated Go/TypeScript bindings and Connect Query own the transport. Repository associations remain explicit and future read-only queries must enforce fresh selected-profile and per-feature authority.

## Change Triggers

Update this document, the credential/protocol/client/desktop contracts, the project index, evidence ledger and scoped AGENTS files when lifecycle, token retention, authority, retry or GitHub capability boundaries change. New GitHub queries/forms/remediation require their own complete permission and real-environment evidence.

## References

- [Project index](project-delidev.md), [complete requirements](cmds-delidev-requirements.md), [credential storage](cmds-delidev-credentials-contract.md), [evidence](cmds-delidev-evidence.md).
- [Repository defaults](repository-defaults.md), [protocol](protos-delidev-v1-contract.md), [desktop](apps-delidev-desktop-contract.md).
- [GitHub authenticated user API](https://docs.github.com/en/rest/users/users#get-the-authenticated-user).
- [GitHub PAT management](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens).
