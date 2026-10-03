# DeliDev ssh setup contract

## Scope

`internal/sshsetup` and authenticated SSHSetupService; Go owns SSH transport and Worker installation orchestration.

## Runtime and Language

Go owns business logic, Connect RPC and durable state. Rust owns trusted-window and OS infrastructure. React uses generated Connect Query bindings.

## Users and Operators

Authorized owners and paired clients; original paired Workers; release maintainers.

## Interfaces and Contracts

`delidev machine ssh` and desktop Runner Device setup inspect a target, display its original host-key fingerprint, require explicit exact-key confirmation, then install/register/start/check only DeliDev Worker. Closed authentication methods use bounded write-only credential input. A changed host key blocks all authenticated commands. Target inspection selects one of six signed Worker artifacts. Repeated setup inspects and preserves the original registration and workspace scope.

Independent System capabilities 28 (signed updates), 29 (SSH setup) and Worker capability 17 are reserved under issue #964 before dependent source changes. Update and SSH metadata use EntityKind 33/34. Reservations alone grant no capability. The approved integrated-PR exception applies; independent branches retain the main-first prerequisite.

## Storage

Credentials use the existing WorkerSSH protected vault purpose. Durable operation metadata contains only original host/port/user, fingerprint, generation, request/actor identities and typed progress. Secrets and pairing documents are transferred only inside encrypted SSH stdin, never shell arguments, logs, ordinary resources or history. Once-only remote journals reconcile uncertain installation or pairing from the original operation.

## Security

No ambient SSH config, agent, known-host fallback, password prompt or arbitrary remote command is permitted. Observe host identity without authentication, and authenticate only the explicitly confirmed original identity. Cancellation closes and joins owned transport/session children; connection loss after a send remains uncertain until original remote status is inspected. Setup cannot remove existing private roots, install harnesses or grant inbound Worker execution ports. Remote Worker uses the existing authenticated outbound pairing/network contracts.

## Logging

Structured logs contain original operation IDs, typed phase/error code, target and public version only. Exclude credentials, host paths, raw SSH output, native content and signing material.

## Build and Test

Run owning Go tests with race/vet, protocol generation checks, generated-client tests, desktop pnpm test and root cargo test after Rust changes. Tests inject temporary stores, SSH peers and signing keys. Test restart/replay, changed identities, cancellation, revocation, signature/target/length/digest rejection and failed replacement separately. Fixtures/builds never establish real remote-host, production signing or installed-platform acceptance. Record source revision, commands, results and remaining limits in PR/CI, not repository evidence files.

## Dependencies and Integrations

golang.org/x/crypto/ssh at the repository security baseline, protected credentials, signed updates and current Worker lifecycle/pairing.

## Change Triggers

Update the project index, allocation ledger and affected domain AGENTS when authority, protocol ownership, supported targets, release namespace, credential lifecycle or installation policy changes.

## References

- [DeliDev project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Structure contract](cmds-delidev-structure-contract.md)
- [Worker network](cmds-delidev-network-contract.md)
- [Credentials](cmds-delidev-credentials-contract.md)
- [Packaging](apps-delidev-packaging-contract.md)
- [Repository defaults](repository-defaults.md)
