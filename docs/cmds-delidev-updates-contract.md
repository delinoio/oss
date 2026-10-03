# DeliDev updates contract

## Scope

`internal/updates`, server UpdateService, Worker replacement, native installation and release manifest tooling.

## Runtime and Language

Go owns business logic, Connect RPC and durable state. Rust owns trusted-window and OS infrastructure. React uses generated Connect Query bindings.

## Users and Operators

Authorized owners and paired clients; original paired Workers; release maintainers.

## Interfaces and Contracts

Authenticated owner/client checks and exact candidate/revision acceptance use UpdateService and `delidev update`. Only stable `delidev-v<semver>` GitHub Releases in `delinoio/oss` supply manifests. A signed manifest covers the complete six-target desktop and six-target Worker inventory, source revision, protocol, byte lengths and SHA-256. Native installation requires a trusted-window confirmation. Worker installation waits for joined active execution, auxiliary, terminal and forwarding ownership. Neither path replaces a live server or a harness.

Independent System capabilities 28 (signed updates), 29 (SSH setup) and Worker capability 17 are reserved under issue #964 before dependent source changes. Update and SSH metadata use EntityKind 33/34. Reservations alone grant no capability. The approved integrated-PR exception applies; independent branches retain the main-first prerequisite.

## Storage

Private bounded candidates retain the original signed manifest and downloaded digest. UUID-v7 receipts bind original actor, server, target, component and revision. Dedicated Worker generation binaries preserve the previous working binary and existing device/workspace scope. A replacement does not modify a shared CLI/server executable. Unknown installation outcomes retain recovery state and cannot claim rollback.

## Security

The compiled DeliDev Ed25519 public declaration is the only manifest authority. An unset or non-production declaration blocks real downloads/installation and release signing; test-only roots cannot activate production. Networking has no ambient proxy, account token, cookie or credentials. Exact GitHub release URLs and bounded HTTPS release-asset redirects are checked independently; cross-project tags, unknown targets, duplicate fields, malformed signatures, stale versions and digest/length mismatch fail before execution. Signing private keys never enter repository, RPC or logs.

## Logging

Structured logs contain original operation IDs, typed phase/error code, target and public version only. Exclude credentials, host paths, raw SSH output, native content and signing material.

## Build and Test

Run owning Go tests with race/vet, protocol generation checks, generated-client tests, desktop pnpm test and root cargo test after Rust changes. Tests inject temporary stores, SSH peers and signing keys. Test restart/replay, changed identities, cancellation, revocation, signature/target/length/digest rejection and failed replacement separately. Fixtures/builds never establish real remote-host, production signing or installed-platform acceptance. Record source revision, commands, results and remaining limits in PR/CI, not repository evidence files.

## Dependencies and Integrations

GitHub Releases, Go crypto/ed25519, SHA-256 and RFC 8785 canonicalization; Rust owns OS installation infrastructure only.

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
