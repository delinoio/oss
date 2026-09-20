# TaskFlow

- Follow `docs/project-taskflow.md` and `docs/crates-taskflow-foundation.md`.
- Keep the library independently testable; CLI commands use the same planner and executor as CI and sessions.
- Never replace native dependency resolution with package-name matching or generate compiler actions.
- Unknown graph coverage expands affected selection conservatively, but unresolved prerequisite selection fails closed. Scope native selector completeness to the owning projects of each adapter invocation.
- Native Cargo selectors evaluate preserved target conditions with Cargo's platform parser and rustc cfg metadata for the selected platform; explicit cargoTarget takes precedence over execution-platform defaults.
- Unix input cache state includes permissions, including file-link target permissions; portable CI structure fingerprints remain content-based.
- Reject empty or option-shaped Git base/head operands before launching diff so revisions cannot alter changed-file selection or write files.
- Validate every affected-mode task filter before intersecting it with changes; a typo must fail even for an empty affected set.
- Never collapse direct/input/schedule causes into a prerequisite cause. Cancellation and invalidation prohibit cache publication.
- Declared output changes override task-reported unchanged; only a matching successful output baseline can suppress propagation.
- Graph output ownership compares missing names using the destination filesystem's actual case/Unicode equivalence, including cross-project ancestors and differently spelled roots of one task; only lexical containment may reuse one task's root. Probes must never mutate declared outputs.
- Before staging artifact contents, probe every path prefix on the destination filesystem to reject case/Unicode aliases and unsupported names without mutating outputs.
- Resolve artifact output declarations to recorded roots using the destination filesystem's actual case/Unicode equivalence, including missing outputs on clean runners. Use those recorded roots for ownership, link validation, and replacement; never normalize all names by case folding or merge aliased artifact records.
- Outputless cached tasks retain a validated semantic result identity separately from the empty file-snapshot digest, including historical cache hits and unchanged reports; sharded result identity includes the tested input version and is shared across partitions while sharded task cache keys remain partition-specific. Ordinary prerequisites never include the enclosing invocation's shard selection in their keys.
- Input hashing streams both regular files and file-link targets through a fixed-size buffer. Internal dangling input links retain target plus missing state; validate their full chain without requiring the target to exist, and never hide permission failures or cycles. Absolute input targets may enter through a canonical alias of the workspace; validate every remaining component. Local output hashing streams file content without artifact transfer limits; version its digest domain independently of encoded payloads. Capture and transport remain bounded.
- Output restoration must validate the complete entry and containment before mutation. Secrets must be masked before persistence.
- Mask each output pipe and the serialized combined task log. Keep the combined redactor alive across pipe EOF and every shard; flush only after all task log owners finish. Normal terminal output shares that combined mask, while show-secrets affects only live output.
- Validate every supplied CI runner label as nonblank and free of control characters during configuration checks and before blueprint generation, including plan/aggregate fallback candidates.
- Validate both CI export destinations through existing filesystem ancestors before writing either workflow or blueprint.
- CI bundle validation verifies intrinsic artifact integrity even for terminal outputs that no downstream job restores.
- CI bundle limits scale with declared artifacts: bound each encoded artifact independently, bound metadata separately, and validate the same limits before publication and after bounded reads.
- CI output transfer requires exact files/directories or complete directory trees even for uncached tasks; reject partial ownership before export, capture, or restoration.
- Local partial output snapshots include only declared matches and require at least one match per pattern; literal traversal anchors and neighboring inputs do not count as outputs. Exact directories and complete directory/** trees retain empty-root ownership.
- Strip workspace-designated secret variables from every task that does not declare them, including tasks grouped into one CI unit. Collect designated values for redaction before that filtering so task-local configuration values remain masked in graph queries.
- Configuration validation checks remote endpoint, bucket, namespace, region, and credential-reference syntax without reading credentials or using the network, including mode off.
- Graph validation rejects remote credential references in every task environment declaration before execution; runtime validation also checks CLI overrides.
- Environment scoping, precedence, masking, fingerprints, runtime retention, and transport credential exclusion use the host's environment-name comparison; Windows names follow its ordinal case-insensitive rules.
- Read inherited environment entries with vars_os and reject non-Unicode names or values with a redacted error before native discovery; never panic or convert their bytes lossily.
- Output capture, local hashing, and cache restoration validate the same link chains. Cache link validation follows archive link chains and existing filesystem ancestors before processing parent components; lexical containment alone cannot authorize restoration.
- Cache link capture and intrinsic validation reject rooted and drive-prefixed targets on every host. Records reject empty targets, NUL, and nonportable backslashes.
- Cache link records use portable separators; Windows link creation converts targets to native separators before invoking the filesystem API.
- Local cache publication rechecks cancellation and inputs under its exclusive lock before and after entry replacement; invalidation restores the previous binding before readers resume, and rollback failures remain failures.
- Local cache readers, writers, and cleaning share a process-safe cache lock outside the removable cache tree; never hold it during commands or network operations.
- Reject semantically invalid local entries before remote fallback selection; valid JSON alone never blocks a compatible remote artifact.
- Cache verification checks artifact identity, content digests, paths, and shard accounting independently of current configuration, rejecting descendants of every non-directory record; restoration additionally validates project ownership and link containment.
- Artifact record paths use one canonical portable spelling: reject dot segments, repeated/trailing separators, backslashes, NUL, and rooted/drive-relative paths before normalized-identity deduplication or restoration.
- Unix commands use the embedded per-command native supervisor; never install a process-wide subreaper in an embedding application. Linux completion requires subreaper ECHILD, and macOS completion requires an isolated launchd resource coalition with zero kernel members. PID enumeration selects signals only; it never proves cleanup. Preserve the pre-execution ownership barrier, private ephemeral command/environment transport, parent-death EOF lease, and setsid/setpgid/double-fork conformance. Do not acknowledge completion or allow replacement before that kernel proof; unsupported native ownership must fail closed.
- Log-owner failure interrupts live services and readiness probes immediately; await the same process ownership future, both drains, and container cleanup before notifying the session. Successful stream EOF does not cancel a service.
- Output-drain failures must still await both stream owners and container removal/absence verification before returning; unverified cleanup takes precedence over log errors.
- Preserve completed, cancelled, and timed-out process reasons separately; finite deadlines produce failed receipts with code 124, while operator cancellation remains code 130. Sharded tasks share one deadline across inventory and all units, and metadata capture must await cleanup on expiry. Docker cleanup clears an expired task deadline and retains its own bounded removal/verification deadline. Shard aggregation retains these reasons and accounts for every remaining unit without launching replacement work.
- Readiness cancellation, readiness deadlines, service deadlines, and early service exit must cancel and await the active probe plus both output drains; no outer timeout may drop that ownership future.
- Validate TCP readiness host/port syntax and HTTP(S) URL shape without DNS or network probes before service startup.
- Every child belongs to a process-tree/container owner; replacement waits for reaping. Tests must assert actual cleanup. Service shutdown must await all owners and propagate every unverified process/container cleanup as failure.
- Ready service receipts expose their semantic task key to dependent cache keys. Session waves reuse finite prerequisite receipts only while current output digests match; missing or modified outputs return to normal restore/execution before consumers run.
- Drain queued service exits before session scheduling and again after prerequisite output validation, before ready receipts can authorize another wave.
- Session task ownership ends on its individual receipt, including provided/suppressed/blocked outcomes. Scope completion notifications to their wave so late results cannot release replacement owners or overwrite newer receipts; retain propagation pending behind active consumers.
- Either watch or schedule initial flag can activate a mixed subscription, with one shared initial execution. Tasks without subscriptions retain initial activation.
- Choose default overlap from the current trigger (input queue, schedule skip), including mixed subscriptions; explicit overlap applies to both. Apply overlap policies to per-task execution ownership, not to membership in an unfinished wave.
- Reject absolute, rooted, and Windows drive-relative input and output patterns during configuration validation on every host, including negative patterns.
- Reject NUL anywhere in complete input/output patterns before compiling globs or deriving traversal anchors, including wildcard suffixes and negative inputs; invalid patterns must never launch prerequisites or commands.
- Reserve ASCII case aliases of .git, .taskflow, and .taskflow-restore-* components on every host, including configuration, output capture, and artifact validation.
- Explicit positive input globs may traverse otherwise ignored trees; prune only using conservative literal directory prefixes, never directory-name substrings. Reserved .git, .taskflow, and .taskflow-restore-* trees remain excluded.
- Empty/disabled/negative-only input declarations read metadata without walking files. Bound automatic scans to the owning project and explicit scans to deduplicated possible literal roots, retaining conservative wildcard/escape coverage and the no-directory-link traversal rule.
- Directory mutation notifications rescan every intersecting positive input root, even when the directory was deleted or does not itself match a glob; only a changed filtered snapshot enqueues work.
- Timestamp delivered watcher events before discovery and preserve matching input causes queued before each baseline, including initial-disabled subscriptions.
- Pre-baseline directory and ambiguous mutation events use positive input-root overlap even when the first snapshot already includes the change. Keep exact filtering for known file events and suppress wholly owned output paths, including deleted output-tree roots.
- Watch invalidation consumes filesystem mutations, never access notifications from metadata discovery or input hashing. Metadata notifications must confirm a changed or invalid graph before cancelling the active generation; identical rewrites preserve live work.
- Filesystem identities are fallible UTF-8 paths; reject invalid bytes in roots, native projects, inputs, outputs, and link targets before matching, hashing, or serialization. Never use lossy conversion for identity keys. Reject literal Unix backslashes before separator normalization.
- Canonicalize watcher roots and event paths before graph matching, including deleted paths through their existing ancestors and Windows path prefixes.
- Notification path normalization must tolerate concurrent removal while preserving broken-link and permission errors; do not separate existence checks from canonicalization. Windows native metadata opens explicitly request attribute-read access and synchronous query completion. Both open and same-handle metadata queries preserve native deletion-pending/file-deleted statuses separately from ACL denial before Win32 error translation; returned errors retain the operation and native status. Windows resolution checks deletion state on the same handle after the final-path lookup, including lookup failures, so NTFS deletion-storage paths never become notification identities.
- Native discovery preserves typed cancellation and cleanup failures through fallbacks; only completed operator cancellation maps top-level errors to exit code 130.
- Go discovery must disable module proxies, checksum databases, VCS, and automatic toolchain downloads; private-module proxy bypass cannot override this boundary.
- Native libtest sharding rejects explicit Cargo --target in either argv form before execution; generic adapters own cross-target runner invocation.
- Rust libtest metadata must honor the command's explicit manifest selection in both argv forms; parse harness declarations only for the selected compiler-artifact targets, never reject unrelated packages or unselected targets.
- Native Go -failfast stops later units across the invocation and records them as skipped without hiding the failure. Native Go shard arguments use an explicit flag/value contract; reject unknown or selection/output flags before inventory execution.
- Real watcher overlap tests start with absent inputs and initial-disabled subscriptions after an activation barrier. Hold the process until a fresh independent subscriber receipt confirms each input version; output content alone can come from an older queued execution, and fixed durations cannot assume bounded OS event-delivery latency. Keep structured event-age, baseline, snapshot-change, and overlap diagnostics available without logging input contents. Initial-execution deduplication fixtures keep the watcher initial-enabled but leave its input absent until the shared prerequisite/companion activation completes.
- Provision Git on PATH for default affected-selection conformance fixtures, including minimal Rust containers.
- Gate MinIO fixture setup on initialized storage, bucket metadata, IAM, and write quorum through /minio/health/cluster; liveness alone cannot authorize S3 commands. Bound the whole readiness wait and each request, fail explicitly on expiry, and log only status/attempt/timing diagnostics.
- Maintain schema freshness and numbered issue #898 conformance scenarios. Report unavailable platform/service evidence accurately.
- Keep `docs/crates-taskflow-conformance.md`, the native/Docker CI matrices, and their centralized `scripts/ci/job-paths.json` ownership synchronized. CI result bundles must prove complete task, artifact, and shard accounting against the exact plan; secret transport is forbidden for PR jobs.
- Docker task commands, tool probes, and shard inventory/execution forward effective explicit CLI overrides alongside task-declared names; never forward the ambient host environment or another task's filtered secrets.
- Docker forwarding selects each effective environment key once using host name comparison. Preserve the resolved spelling; never expand Windows declaration aliases into distinct Linux container variables.
- Retain the validated Docker launch environment and working directory for awaited removal, absence verification, and destructor fallback.
- Reject blank, option-shaped, or NUL-containing Docker port mappings during configuration validation, before prerequisites or Docker setup can run. Preserve supported Docker publish syntax without rewriting it.
- Docker image references require one digest marker with a complete 64-character lowercase SHA-256 suffix; reject malformed digest syntax during configuration checks.
- Validate Docker OS selection after CLI defaults are applied; explicit task components retain precedence.
- Validate the effective Docker context endpoint with the exact launch environment; a local DOCKER_HOST cannot authorize an overriding remote context.
- Tool identities hash bounded stdout and stderr separately; native metadata parsers consume stdout only.
- Probe tools inside the selected Docker image. Docker result reporting must use a container-compatible helper, never the host binary. Remote publication stages content before rechecking cancellation/input state. Guarded local publication is the completion boundary; persist its successful receipt before a bounded, awaited remote manifest PUT, and never retroactively cancel that completed task even if the PUT response is lost. Finite invocation success and CLI exit codes follow finalized receipts, not a late cancellation token; pending cancellations still produce code 130. Session failures await all child owners before returning.
- Existing repository workflows are not migrated and the crate remains unpublished.

- CI Rust numeric versions must be valid unprefixed rustup toolchain names; reject v-prefixed versions before workflow files are written.
- Reject simultaneous explicit changed files and Git base/head selectors before planning or prerequisite execution; neither selection may silently override the other.
- Bound artifact capture incrementally by encoded record size, including empty entries, escaped paths, links, digests, and Base64 expansion, before retaining records or reading payloads. Keep local uncached identity hashing independent of transfer bounds.
- Critical-path duration traversal must count a shared suffix on every alternative branch; cycle guards track only current ancestry, not all visited nodes.
- Docker libtest inventories must reject compiler-artifact executables outside the persistent /workspace mount or absent after build-container removal, before launching test listing. Test Cargo config, environment, and argv target directories in real Docker conformance.
- Historical cache shard evidence must contain exactly one partition or a complete suite. Integrity verification rejects other partial cardinalities without relying on current task configuration.
- Sanitize dotenv errors before returning from environment construction: parser lines and error source chains can contain secrets before redaction values exist. Diagnostics may contain the file path and logical-line byte offset, never dotenv contents.
- Suppressing prerequisite-only work requires a successful baseline even without declared outputs. Invalidate the persisted baseline under the task lock before each new attempt, retaining it only in memory for output comparison; failures and interrupted attempts must not expose an older success to later suppression.
- Suppression must recompute the executor's full task identity under the task/resource locks and compare it with that successful baseline, including environment, tool identities, input state, configuration, and shard partition. Preserve the baseline only after both identity and outputs validate.

- Check cancellation before task setup, before each resource lock attempt, after lock acquisition, and immediately before command launch. Pending work must not invalidate a prior baseline or launch side effects; acquired Docker ownership still requires awaited cleanup.

- Bootstrap receipts cross native graph rediscovery only after their refreshed task keys, declared outputs, and prerequisite receipts match. Reuse the executor's identity calculation; invalidate dependent reuse transitively and give stale tasks an independent activation cause. Shut down bootstrap services before rediscovery.
- Finite invocation and CI execution exit codes preserve receipt reasons: success is 0, operator cancellation takes priority as 130, and any failed receipt with code 124 makes an otherwise failed invocation return 124 before ordinary failure code 1.

- Preserve Git raw old/new gitlink modes for affected selection. Submodule changes select intersecting positive input trees even without a checkout; ordinary missing files retain exact matching and wholly owned outputs remain excluded.

- Docker named-pipe endpoints must use the literal local dot server; the npipe scheme alone does not prove locality. Reject remote authorities and malformed socket addresses before creating execution state.

- Container result helpers use scoped temporary directories owned through cleanup, including tool probes and shard units. Auxiliary containers must not create permanent run directories; outer task results and logs remain retained.

- Graph refresh compares watched input snapshots before replacing the baseline and preserves pending independent causes. Automatic metadata changes activate initial-disabled watchers; invalid configuration retains the last accepted snapshots until recovery. Identical metadata rewrites do not enqueue work.

- Non-root project configurations may declare project identity, tasks, and dotenv policy. Reject nondefault workspace/start and any remote/ci configuration instead of silently ignoring root-only policy. Diagnostics identify the child configuration and field without configuration values.

- Generic shard result files share the structured-metadata byte limit with inventory output. Validate regular-file size and bound the actual read before JSON decoding so concurrent growth cannot bypass the cap.

- Session-provided receipts retain their validated semantic identity but clear historical changed flags for each new wave, including ready services. Only results produced by the current wave propagate changes.

- Run selected install roots in topological bootstrap phases even when native metadata is already complete. Rediscover and replan between roots, revalidate all retained receipts, and reject a repeated unstabilized install in one graph generation before executing stale dependents.

- Revalidate installation receipts using the same unsharded bootstrap options that produced them, including when a development session selects one test partition.

- Failed installation phases retain their RunResult receipts and termination status in run and start; timeout is 124 and cancellation is 130. Await session service cleanup before returning that result, and let unverified cleanup remain a failure.

- Validate automatic metadata inputs with the same complete link-chain containment as declared inputs before hashing discovery generations, CI manifests, or task snapshots, including input-disabled tasks and missing targets.

- Persist selected OS/architecture defaults in CI blueprints and reapply them before graph construction on runners. Explicit task components retain precedence; conditional prerequisites, causes, and unit boundaries must match export.

- Native owner bootstraps use a closed system environment. Transport the bounded task environment privately over the EOF lease and apply it only after kernel ownership exists; loader hooks must never execute in the bootstrap helper. Never persist transport values in files, launchd plists, or diagnostics.

- Linux launches the embedded supervisor from a sealed executable memfd, retaining its close-on-exec descriptor through spawn. Temporary control storage may be noexec. Unsupported MFD_EXEC flags may use the legacy kernel API; explicit executable-memory policy denials must fail closed without disk fallback.

- Preserve structured launch error kind and OS code without command/environment contents. Linux conformance overlaps supervisor launches across threads to cover inherited writable-file ETXTBSY regressions.

- Cache reuse is finalized by its durable receipt; cancellation after successful persistence cannot change that receipt or invocation to cancelled. Cancellation before publication still invalidates reuse.

- Apply the same regular-file and bounded-read checks to persisted shard inventories and every shard report as to generic adapter results; reject special files before blocking opens and cap actual reads as well as metadata lengths.

- Docker shard execution preserves validated task port mappings for each owned unit; only inventory and tool probes omit publishing. Await unit cleanup before the next unit reuses a port.

- CI secret and remote-credential names cannot collide with generated control variables under the execution unit's target OS name semantics, independent of the exporter host. Validate before writing workflow or blueprint.

- Accumulate invalidated session bootstrap task IDs across installation phases, including initial-disabled watch roots. Remove an ID only after a later refreshed receipt is retained, and activate remaining selected IDs after bootstrap.

- Linux image-seal conformance must run without root privileges. Exercise seals through the retained creation descriptor; do not require reopening a mode-0500 executable for writing.

- Execution result reports are optional regular files capped at 1 KiB. Reject links, reparse points, and special files without blocking; bound both the opened file length and actual read before parsing and execution-ID validation.

- Retain stopped bootstrap services only as revalidated semantic evidence for their installers across every phase. Exclude that evidence from executable provided receipts: every later phase or session requiring readiness must create a fresh owned service. Service identity changes invalidate dependent installation evidence.

- Windows cleanup checks TerminateJobObject and job accounting, awaits zero active processes and direct-child reaping, then marks ownership clean. API/query/deadline failures remain CleanupFailure, leave Drop retry enabled, and cannot authorize successful receipts or replacement. Preserve kill-on-close as a final fallback.

- Concurrent conformance tasks use separate receipt/marker files when asserting per-task line counts; uncoordinated append writes are not an atomic event log.

- Windows cleanup fixtures retain the descendant's kernel handle before termination and require it to be signaled when cleanup returns. Check port release separately with bounded AddrInUse retries only after that proof; never use SO_REUSEADDR or PID reopening as cleanup evidence. Let descendants bind their own ephemeral sockets before atomically publishing readiness.

- Persisted receipts are regular no-follow files capped at 1 MiB on publication and read. Invalid prior receipts cannot block scheduling or establish a successful baseline.
