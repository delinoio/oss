# React Forge MCP Contract

## Scope
`packages/react-forge/src/mcp` owns the React Forge stdio server, shared Node execution process, TSX loader and session registry. The private source workspace assembles these modules into the public npm package. The explicit MCP follow-up supersedes the original issue #968 exclusion of a new MCP interface. Existing Forge MCP, React Forge library and one-shot CLI behavior remain supported.

## Runtime and Language
Use the existing Node.js 24, React 19.2.8 and six-host native inventory. TypeScript owns MCP and React execution; validated native computations remain in the existing Rust workers. All sessions share one execution process and canonical React/React Forge imports, including Figma's per-authentication rate admission and per-file publication queues. Authenticated Figma retains its existing macOS Keychain boundary; this extension does not claim new Figma authentication platforms.

## Users and Operators
Node.js developers and local MCP clients executing trusted document tasks. Start the installed package with `react-forge mcp [--cwd <directory>]`. The working directory defaults to the launch directory and must exist. There is no HTTP listener, hosted service or automatic client configuration change.

## Interfaces and Contracts
The official TypeScript MCP SDK owns stdio framing and protocol negotiation. The server exposes tools only, using the `react_forge_` prefix:

| Suffix | Input and behavior |
| --- | --- |
| `capabilities` | No arguments; existing format/runtime limits plus MCP source, inspection, trust and lifecycle contract. |
| `execute` | Exactly one of `code` or `entry`, optional `sessionId` and JSON `data`. Evaluates a fresh TSX entry, then calls its default export with `{ session, state, data, signal }`. |
| `sessions` | `offset` and `limit`; lists active session IDs, formats, current revisions and idle/running/closing status. |
| `inspect` | `sessionId`, optional `nodeId`/`kind`, `offset`, `limit`, and `view` (`targets` or `receipt`). Local targets come from a settled snapshot; Figma targets are cached. Receipt view never republishes. |
| `measure` | `sessionId`, `nodeId`, exact `revision`. Local callers obtain the revision through inspect; Figma requires a completed published revision. |
| `refresh` | Figma `sessionId`, optional `pageId`, up to 24 `nodeIds`, and `resources`. Delegates to the existing scoped remote reader. |
| `export` | Local session, `output`, optional `overwrite` (default false). Extension must match PPTX/DOCX/XLSX/PDF/WAV, or `.sprite.zip` for sprites. |
| `publish` | Figma session, optional `receiptPath` ending in `.figma.json`, optional `overwrite` (default false). Without a path, publishes without saving a local receipt. |
| `close` | `sessionId`; serializes behind prior work, disposes the session, and clears its state. |

`sessionId` is the session's lowercase UUID-v7 `documentId`. A new execute call must return a live `DocumentSession` or `FigmaSession`; an update returns void or that same object. Returning another session never replaces the selected one; an unregistered rejected replacement is disposed. Session-owned `state` is a persistent `Map<string, unknown>` for components, setters, refs, mount handles and caller state. It is never serialized. `McpTaskContext` and `McpSessionTask` are type-only package exports. Tasks own cleanup for resources they create and throw away before returning a session.

Inline imports and tool-relative paths resolve against the fixed working directory; file-relative imports and `import.meta.url` resolve against the entry's directory. The loader evaluates each entry under a fresh virtual file URL without writing code to disk. Inline entries, file entries and typed helpers use the automatic React JSX runtime; the loader does not apply caller `tsconfig` JSX settings during compilation. tsx supplies dependency resolution. Imported dependencies remain cached for the process lifetime; this is not a watch/reload service. The loader pins public React/React Forge imports to the running package to prevent duplicate reconciler and Figma scheduler instances. Arbitrary caller dependencies retain normal Node resolution and permissions.

The synchronous MCP load hook must provide source bytes for resolved file-backed CommonJS modules when the `tsx` hook chain would otherwise return an undefined source. This includes the running package's `react/jsx-runtime` and its CommonJS implementation. Keep the resolved URL and CommonJS format so React identity and ordinary caller dependency resolution remain intact; remove the direct-read workaround only when the supported Node/`tsx` hook chain supplies valid sources itself.

`DocumentSession.snapshot({ signal })` settles React roots, pending mounts and registered assets, pins the existing model/font revision and returns the corresponding inspection captured at that boundary without generating or publishing output. The synchronous `inspect()` remains available. A snapshot is not native layout certification; measure/export still perform native validation. Later React commits can invalidate an earlier measurement revision.

Successful results contain `structuredContent` and equivalent JSON text. Failures use `isError` and existing `ForgeError` codes/context; Figma publication failures additionally include their original receipt. The receipt's status is authoritative: for example a remote setter error can have code `remote` and status `partial`. A saved receipt is not a `.fig` file. Each result identifies the applicable session/format/revision when available. Geometry and successful publication results retain their pinned revision.

For caller TSX failures, MCP error results additionally expose a bounded `error.message` and optional `error.diagnostics` entries. Each entry has `phase` (`compile`, `task`, or `render`), `message`, and, when proven, `source` (`inline`, `entry`, or `import`), a path relative to `--cwd` in `file`, and one-based `line`/`column`. Local JavaScript helpers reached through caller imports also retain proven task-exception locations; unrelated dependency and engine modules do not gain caller provenance. For exceptions, including React renderer causes, the first file-backed throw-site frame must be caller-owned; a later caller call-site frame cannot authorize disclosure of a dependency's or Node built-in operation's message. Thrown values without a traceable stack, including strings, retain generic error text. Compiler syntax and module-resolution failures retain code `malformed_input`; uncaught React rendering and caller task exceptions retain code `render`. Resolution messages use a safe caller import specifier when available, never Node's host-path-bearing resolver text. Return at most ten compiler errors and set `error.diagnosticsTruncated` if more exist. Individual messages are capped at 1024 JavaScript characters; source excerpts and full stacks are omitted. Absence of a reliable source position leaves location fields absent. Error boundaries that recover do not produce tool failures. Existing native/model `ForgeError` classifications and Figma receipts retain their current shape.

Inspection and session listing default to 100 items and allow at most 500 per page. `total`, `truncated` and `nextOffset` expose pagination. Target text previews stop at 4096 JavaScript characters and include `textTruncated`; underlying document content is unchanged. Receipt view returns the full bounded, credential-free native receipt, including bindings and outcome.
Generated PDF paragraph targets expose their rendered text to inspection after the session settles, including on later revisions.

Calls on one session execute in admission order. Request cancellation reaches its AbortSignal but never releases the queue before the callback actually settles. Independent sessions may progress concurrently. No callback-wide transaction or rollback is promised: completed renders, file writes and remote batches remain completed when subsequent code fails. Built-in export/publish retain atomic local publication, original-source protection, revision pinning, and truthful partial/unknown Figma results. Execute never automatically exports or publishes, but trusted caller code may explicitly do either.

EOF, transport close, SIGINT/SIGTERM and Windows SIGBREAK stop admission, abort active requests and dispose sessions. Shutdown alone has a five-second grace period, after which the parent terminates and reaps the execution child. Unix exit codes remain 130/143 and Windows Ctrl+C/Ctrl+Break use 130. Ordinary operations have no automatic timeout. A synchronous loop can prevent cooperative cancellation; the parent remains able to handle protocol traffic and terminate the execution process on shutdown. Forceful host termination cannot guarantee cleanup. Worker loss ends the server with status 1, invalidates all memory sessions, and reports `unknown_outcome` for outstanding requests when the transport is still available; it never restarts or retries writes. Clients must inspect exported files or remote state before retrying uncertain work.

## Storage
Sessions, state Maps and source text are memory-only. Node's evaluated module cache lives until the execution process exits; closing a document releases its owned session resources but is not an ESM module-cache eviction guarantee. Explicit exports/receipts use the existing local-file exception to R2 storage. No recovery database or revision archive is added. Disposal never deletes exported files or remote content. Imported sources and canonical aliases remain protected even when overwrite is true.

## Security
TSX is trusted code running with the caller's permissions, not a sandbox. The subprocess boundary protects MCP transport and diagnostics, not the filesystem, network or credentials from deliberate caller code. No HTTP authentication surface or credentials are added. Figma continues to use only its selected existing host credential, with the existing single reread allowance and no refresh/writeback.

The UTF-8 TSX entry plus serialized JSON data is capped at 16 MiB before evaluation; file entries must be regular `.tsx` files and use bounded reads. Existing document/image/package limits remain in force. The stdio read buffer also bounds unfinished frames while allowing sixfold JSON escaping plus a small envelope allowance. State Maps and arbitrary caller dependencies are caller-owned trusted code, not process-wide resource governance. No implicit asset downloads or runtime installations are introduced.

## Logging
The parent emits structured stderr diagnostics containing fixed operation/stage, duration and stable error classification. The execution child's stdout/stderr, including direct descriptor writes and task cleanup output, are disconnected from both protocol output and operational logs. The sole exception for caller exception text is the bounded, explicit MCP tool error result described above; that message can itself contain sensitive caller data. Never relay arbitrary exceptions, task text, paths, XML, asset bytes or credentials to operational logs. Do not add full stacks, source excerpts or absolute host paths as structured error fields. SDK protocol errors are logged using a fixed classification without their raw message. stdout contains only MCP messages.

## Build and Test
Build the native binding and TypeScript output before server or packed-consumer tests. Run package build, typecheck, lint, test and `typecheck:examples`. All six existing CI hosts execute real SDK stdio tests and installed consumers for the four local formats and fake Figma; standalone examples are checked after the build. Existing CLI, React, native integration and preservation tests remain required.

MCP coverage includes relative/inline imports, module singleton identity, fresh entry evaluation, actual React hook/Suspense state, mounted Office edits, exact revision measurement, original/output conflicts, filtered pagination, malformed/oversized source, bounded inline/file/import compiler diagnostics, module-resolution and callback exceptions, uncaught render detail and recovery, stderr redaction, direct stdout writes, queued cancellation, late returned-session cleanup, EOF/signals, a blocked event loop, worker loss, and Figma complete/partial/unknown publication receipts without blind retries. Offline Figma tests use synthetic credentials/fake MCP only. Remove generated `dist` after validation.

The real stdio MCP test must create and inspect a PDF from inline JSX without a React import, update the same session through a second inline JSX call, and verify its text and revision. A `React.createElement` control must exercise an ordinary caller CommonJS dependency and canonical React identity. Keep the plain-error redaction assertion alongside these success cases.

Cancellation fixtures register their abort waiter before publishing readiness and handle an already-aborted signal. The late-return fixture deliberately resumes after cancellation has arrived, proving cleanup does not depend on the timing of the readiness write's continuation.

## Dependencies and Integrations
Reuse pinned `@modelcontextprotocol/sdk`, `tsx` and the existing native engines. Declare exact direct `esbuild` and `zod` dependencies matching the existing lockfile versions for virtual TSX transformation and shared runtime/advertised schemas. The parent alone owns protocol stdout; one execution child owns all sessions. No existing Forge CLI/MCP or native defaults change.

## Change Triggers
Keep this contract, Node/Figma contracts, original requirements follow-up, project/docs indexes, relevant AGENTS rules, README, tool descriptions and installed-consumer tests synchronized when interfaces or boundaries change.

## References
- [Project](project-react-forge.md).
- [Node sessions and CLI](packages-react-forge-contract.md).
- [Figma publication](packages-react-forge-figma-contract.md).
- [Repository defaults](repository-defaults.md).
- [MCP server and stdio logging guidance](https://modelcontextprotocol.io/docs/2026-07-28/develop/build-server).

## Static 3D extension (Published in npm 0.2.0)

The generation-only GLB/FBX extension follows [the scene contract](packages-react-forge-scene-contract.md). `SceneSession` shares local publication and MCP lifecycle, uses independent world-space bounds and native scene engines, and introduces no runtime conversion dependency.

## Sprite Follow-up (Published in npm 0.2.0)
The sprite extension reuses `DocumentSession` and all existing tools: `execute` resolves `/sprite` to the canonical package, `inspect` exposes authored nodes, `measure` reports logical frame geometry, and `export` publishes one `.sprite.zip`. No additional tools or asset downloads are introduced. Both inline-source tests and installed-consumer CLI/MCP tests exercise it. See the [sprite contract](packages-react-forge-sprite-contract.md).

## SFX follow-up (Published in npm 0.2.0)

`Format.Wav` and canonical `@delino/react-forge/sfx` imports use existing execute/inspect/measure/export/close tools. Capabilities expose WAV encoding and synthesis bounds; measure returns timeline x/width in seconds at the pinned revision. The [SFX contract](packages-react-forge-sfx-contract.md) defines generation-only behavior. Installed CLI and SDK MCP consumers exercise the zombie-game gunshot without external samples or audio playback.
