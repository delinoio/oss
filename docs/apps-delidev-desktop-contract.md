# DeliDev desktop client

## Scope
`apps/delidev` owns the React desktop presentation and native Tauri host for DeliDev. The full desktop requirements in issue #964 remain normative; implemented surfaces and remaining native/product work are recorded in the evidence ledger.

## Runtime and Language
React 19.2.8 and TypeScript render the trusted app through Rsbuild. The native Tauri component is reserved for OS presentation and bundled Go sidecar startup/supervision; it is not yet implemented in this frontend increment. Go owns every product operation. The frontend development origin is fixed at `http://127.0.0.1:46311`; conflicts fail. This is a desktop application, not a browser product or deployed website.

## Users and Operators
One server owner can connect multiple paired desktop clients. The initial prerequisite checklist links to saved settings without installing a harness or overriding existing setup. Automated readiness checks remain pending.

## Interfaces and Contracts
- Use `@delinoio/delidev-api-client` and generated service-specific `@connectrpc/connect-query` descriptors for direct authenticated Connect RPC. No Rust agent traffic proxy or duplicate eligibility/routing engine is permitted.
- Preserve independent outcome, Archive, dispatch and recovery states. Actions use original resource revisions and stable request IDs; uncertain retries reuse the original immutable request, never another mutation identity. Restore cannot imply Resume.
- Keep drafts mounted across settings and supporting-surface navigation. Settings is an accessible modal with Escape, contained focus and focus restoration. Workspace icons have explicit accessible names in addition to appearance.
- Conversation content is inert text. Never render native content as HTML, execute a returned command or open arbitrary links automatically. Transcript tools/artifacts remain distinguishable from user/assistant text.
- Search, activity and inbox use bounded pages. Inbox reading and answering remain separate. Unknown/unsupported native capabilities are explicit and cannot enable an emulated action.
- Native initialization, local server lifetime, protected pairing, tray, notification/widget, signed update and session-side-app requirements must each have implementation and actual platform evidence before desktop completion is claimed.

## Storage
The server remains the only database owner. Frontend query caches and unsent drafts are memory-only and scoped to the selected connection. No credential, prompt, transcript, cursor or account browser state enters Web Storage. Client exit cannot stop server-owned sessions. Native profiles and server startup are separate infrastructure boundaries.

## Security
Only trusted app content receives native capabilities. Renderer/server calls require exact allowed origins and the explicitly selected connection. Account credentials and GitHub PATs must never enter read responses. Never expose a shell, arbitrary executable/file reader, network proxy, or secret-bearing diagnostic object to the renderer.

## Logging
Expose typed safe problems and correlation IDs, plus independent connection/retry state. Never log input, resource documents, tokens, native output or account locators. Native logs use stable operation/failure classifications.

## Build and Test
Frontend changes require package-local `pnpm test`, typechecking and production Rsbuild verification. Component tests use real generated Connect router transports and explicit test credentials, exercising draft preservation, revision/request identity, authentication failure and inert content. Native Rust work additionally requires root Cargo tests and host-specific lifecycle checks. Generated `dist` is removed from the final worktree.

## Dependencies and Integrations
Use the repository's React, Connect Query, React Query and Rsbuild pins. The Go server/Worker and canonical versioned protobuf remain authoritative. Toss frontend guidelines inform explicit status, focused forms, clear action hierarchy and accessible components.

## Change Triggers
Update app/root AGENTS, project ownership, the client/protocol contracts, build/CI configuration and evidence ledger with path, interface, lifecycle, security or supported-platform changes. Never conflate a component fixture or build with native packaged acceptance.

## References
- [Project](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [TypeScript client](packages-delidev-api-client-contract.md)
- [Evidence](cmds-delidev-evidence.md)
- [Repository defaults](repository-defaults.md)
