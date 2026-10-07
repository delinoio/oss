# DeliDev runtime ownership observations

## Scope
This contract defines runtime ownership behavior across the DeliDev Go server,
CLI, Worker, native desktop and frontend. It supersedes runtime role restrictions,
original-owner equality requirements and cleanup-proof admission gates in earlier
DeliDev contracts. Source-code ownership and component responsibilities remain
separate.

## Runtime and Language
Go owns business behavior. Rust owns native desktop control. TypeScript presents
server observations and actions. Use existing structured logging facilities.

## Users and Operators
The server owner and every currently registered paired client or Worker have the
same product access. Device type, original initiator, assigned machine and process
identity remain metadata; they do not grant separate product permissions.

## Interfaces and Contracts
Keep server token authentication, pairing and registration revocation. Recheck
current registration inside transactions and on receipt replay. An invalid or
revoked credential remains rejected. Authentication is not an original-resource
ownership comparison.

Ownership mismatches must not block start, reporting, retry, Resume, restart,
resource access, deletion or cleanup. Observe them without changing the selected
account, model or Worker, rewriting accepted assignments, or rewriting historical
attribution. Actual input validation, missing resources, revision conflicts,
request-ID deduplication and negotiated protocol support remain enforced.

Unconfirmed native cleanup does not block subsequent admission. Preserve Stop
intent, existing deadlines and retry-attempt limits. A prior native attempt may
still be running when a later attempt is admitted. Explicit Resume without queued
input creates a new input and execution attempt from the retained input; it leaves
the prior input, assignment and execution history intact. Request receipts and
revision checks prevent duplicate admission of that explicit action. Never publish
unconfirmed cleanup or an unknown native outcome as positively confirmed success.

Terminate only through retained original native process, job or control handles.
A retained PID, process-birth sample, service label or journal cannot reconstruct
termination authority. Without a retained handle, observe the old execution and
continue admission without signaling it.

Keep existing RPCs, wire numbers, stored fields and portable imports. Ownership
fields remain metadata and compatibility inputs. Receipt replay may differ only
in typed principal attribution; retain the exact original digest and result, and
continue to reject changed payloads, selected references and revisions. Read
retained device descriptors for legacy attribution comparison without using them
as an authentication source. Add no migration or format
conversion. Remove proof-only production, transfer, waits and UI gates when their
only purpose was ownership enforcement.

## Storage
Create new state with existing private defaults. Existing filesystem owners,
Unix mode sharing and Windows ACL sharing do not prevent access. Preserve actual
operating-system access failures, path boundaries, symlink refusal, file types,
atomic writes, revision checks and immutable stored execution references.

## Security
Retain credential encryption, cryptographic authentication, TLS/origin handling,
secret redaction and registration revocation. Remove additional product access
restrictions based on authenticated role or original resource ownership. Protected
values never enter ownership observations or diagnostics.

## Logging
Record the operation/reference UUID, closed check kind, unconfirmed observation
and next action with Go `slog` or Rust `tracing`. Do not log paths, tokens, raw
native output or account/provider content. Observations do not fabricate proof.
Actual execution, read, write and removal failures remain errors.

## Build and Test
Test authenticated cross-role and cross-owner access, transaction-time revocation,
receipt replay, revisions and original historical attribution. Test missing and
mismatched journals, retained-handle cancellation, and cold recovery without
signaling. Assert that unknown cleanup remains unknown while admission proceeds.
Cover real filesystem errors and preserved path/file validation independently.

Run relevant Go tests, race tests and vet. Run root `cargo test` after Rust changes
and `pnpm test` in the DeliDev frontend after frontend changes. Hydrate required
LFS assets and prepare generated inputs. Remove generated repository-owned `dist`
directories afterward. Distinguish fixtures and cross-builds from actual native
platform/account acceptance in PRs and CI logs/artifacts.

## Dependencies and Integrations
Authentication and pairing use the existing Connect and store boundaries. Native
control retains the existing platform handles. Component contracts retain their
non-ownership input, format, lifecycle and immutable-history requirements.

## Change Triggers
Update this contract, the project index and relevant `AGENTS.md` when ownership
observation policy, authentication, retained-handle control or cleanup reporting
changes. Keep implementation and validation records in PRs and CI artifacts.

## References
- [DeliDev project](project-delidev.md)
- [CLI/server/Worker](cmds-delidev-contract.md)
- [Native process lifecycle](cmds-delidev-process-contract.md)
- [Repository defaults](repository-defaults.md)
