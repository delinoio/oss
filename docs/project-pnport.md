# pnport

## Owner-authorized 0.1.0 publication amendment (2026-10-04)

The owner explicitly requested skipping investigation and fixes for the recorded
macOS initialization and ordinary SIGHUP failures, the separate root clibox watch
failure, and the complete-requirements acceptance review, then proceeding with
formal 0.1.0 release preparation. This is a publication exception for exactly
0.1.0, not a cause fix or a claim that the skipped checks passed. Keep their
failure records and incomplete acceptance visible in PRs, issue #958 and release
notes. Do not close #958; its complete requirements, including Windows 0.2.0,
remain in scope.

Set the private publication declaration to `pnportReleaseReady: true` and
`pnportReleaseVersion: "0.1.0"`. The publisher requires both the boolean and an
exact stable source-version match. The coordinator preserves these declarations
and performs the normal version-only promotion from 0.1.0-next.1 to 0.1.0.
Later versions require their own reviewed authorization. Tag pushes run credential-free dry runs only. After the final tag passes, publish by an explicit `Release pnport` dispatch at that same tag with `dry_run=false`; all publication jobs require the dispatch event and retain fresh native verification.

The final 0.1.0 build must still pass all four native candidate jobs on macOS 15
and Ubuntu 22.04, installed npm/Yarn and direct-install checks, default-parallel
installed lifecycle/native conformance, inline/split peer TypeScript, numeric
benchmarks, complete-set integrity and a nonpublishing dry run. New failures in
these retained gates still block publication. Preserve native-before-launcher
npm order, provenance, signed GitHub assets and immutable retries. Keep the
published 0.1.0-next.1 bytes and npm next channel unchanged. Public guides
describe verified stable 0.1.0 distribution with the unresolved macOS and
full-acceptance limits. This amendment takes precedence over earlier statements that full acceptance must be
complete before the first stable publication.

Public README/release notes disclose platforms and unresolved user-facing failures;
keep approval and pipeline details in internal contracts, PRs, issues and CI.
Before any publication outputs or write authority, the prepare job must verify
the latest first-party tag-push run for the exact tag/revision succeeded, with
all four native jobs and complete assembly successful and every publication
job skipped. Discovery and job readback fail closed, including missing, pending,
failed, ambiguous and untrusted results. Branch candidates do not grant this
authority.

## Goal
Run PnP-unaware subprocesses against an installed Yarn 4 dependency graph without creating a project node_modules directory. Issue #958 defines the complete staged acceptance boundary: 0.1.0 ships macOS/glibc Linux first, and 0.2.0 adds Windows. The owner-authorized experimental npm next channel permits an exact reviewed `0.1.0-next.N` candidate for external testing, without completing stable acceptance; the original Windows requirements remain in scope.

## Project ID
`Pnport = "pnport"`.

## Domain Ownership Map
- Rust: `crates/pnport`, `crates/pnport-core`, and `crates/pnport-preload`, plus the private fspy source fork. The macOS injection library is built from `fspy_preload_unix`; Linux and Windows retain `pnport-preload`.
- Packages: `packages/pnport`, the private source of the npm launcher and four native optional packages for 0.1.0. Windows adds its two native packages in 0.2.0.
- Apps: `apps/public-docs/docs/pnport`, the consolidated public guides at `https://oss.delino.io/pnport`, describing verified stable 0.1.0 availability with unresolved macOS and full-acceptance limits, and distinguishing the immutable experimental preview from stable distribution.
- The public `/pnport/preview-testing` guide owns reproducible Turbopack and TypeScript 7 `tsc` commands for external preview testers, with exact published pnport/compiler versions and known platform and acceptance limits.

## Domain Contract Documents
- [Rust foundation](crates-pnport-foundation.md)
- [fspy source fork](crates-fspy-vendor-contract.md)
- [Complete accepted requirements](crates-pnport-requirements.md)
- [npm distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md)

## Cross-Domain Invariants
- Version 0.1.0 targets macOS 15+ and Ubuntu 22.04-equivalent glibc on x64/arm64. Version 0.2.0 adds Windows 10 22H2+ MSVC on x64/arm64 after its full native acceptance. Linux static children are a release gate. Musl hosts and mixed architectures are excluded.
- Use Rust, as explicitly required by #958, instead of the default Go. Retain nightly-2026-01-01 and protected Tauri dependencies. All owned Cargo packages are private.
- Use pnp exactly 0.12.12 and fork the required local fspy crates from 3aac49e31fba6905bb0b3d0e29d7755493241e9c with retained provenance and licensing. No upstream release is required.
- Private local content-addressed cache storage replaces the default remote R2 storage because native filesystem backing must remain offline and user-local. SHA-256 identities, rather than UUIDs, identify immutable content; execution identities use UUID v7.
- No runtime networking, service, account, telemetry, feature flags, installer lifecycle scripts, automatic updates, or public Rust/JavaScript API.
- Native/npm versions agree; release identity is pnport@v<MAJOR.MINOR.PATCH>, or pnport@v0.1.0-next.N for the explicitly authorized experimental channel. Preserve 0.1.x command and diagnostic compatibility. Native artifacts, installers, Homebrew, npm, conformance and documentation must pass every target in the selected release before publication: four for 0.1.0 and six when Windows is added in 0.2.0.
- macOS CLI and distribution inspection require private companion ABI format 3 for constructor launch leases, while Linux/Windows retain format 1. A native package always contains the executable and companion from the same verified build; reject older macOS companions before user launch without changing CLI commands or doctor JSON v1.
- Before the first release preparation, the source version is `0.0.0` in all three pnport Cargo packages, their lock entries, and the npm source. Release Project rejects patch or major bumps from this source; the first minor bump produces `0.1.0`, and pnport-only next produces the exact reviewed `pnportPreviewVersion` (`0.1.0-next.1` initially). Later next preparations require a new exact reviewed declaration; a minor promotion from the preview line produces `0.1.0`. It creates the version commit and tag through the common coordinator path. The separate pnport tag workflow requires complete four-host evidence before 0.1.0 publication. CI and release matrices derive from the package-owned target registry; the private source `pnportReleaseReady` gate normally stays false until full conformance, minimum-OS and benchmark acceptance is reviewed. The version-specific 2026-10-04 amendment authorizes exactly 0.1.0 after its retained final gates; the current source version is 0.1.0. No pnport release uses crates.io or Cargo registry credentials.
- Unsupported interception fails closed. Never substitute a protected executable, elevate privileges, silently run without virtualization, or claim universal executable compatibility.
- Native library constructors retain the same virtual filesystem as ordinary child code. macOS releases translation ownership before dyld callbacks; four-host installed conformance includes nested ZIP libraries/fork callbacks, ZIP package bins, mmap/read-only data and actual source-file/directory watches. Focused fixture success does not complete the separate tree-ownership, minimum-OS or benchmark gates.
- The macOS owner admits session/group changes only after native birth/version registration and uses audit-token signals for additional groups. Preserve the original group reservation, authenticated crash recovery and protected context through environment replacement. Before publishing a signed admission, publish its complete native identity in a fixed-size shared mapping backed by an unlinked private file, inherited solely by the authenticated same-image guardian. The supervisor mapping is read-only; retain decoded versions in supervisor-owned memory and drain all bounded completed slots before guardian-loss recovery. Deletion of writable journal files must never erase historical ancestry. A release/acquire publication count exposes only complete immutable slots; a partial final slot never grants ownership. The retained mapping survives guardian exit and accommodates every bounded version without socket backpressure or a stopped supervisor acknowledgement. Bound slots to 1,024-byte payloads and history to 65,536 versions; failed replication closes new admission while proven cleanup authority remains available. Never expose the descriptor to user children or obtain authority from an incomplete copy. Invalid journal content still fails foreground recovery before tty transfer; private admitted history remains available for shutdown. Group/session admission verifies signed acknowledgements using only an injected public key, and tty restoration proves any descendant-selected foreground group. A job that actually claimed the tty may also restore its currently verified empty foreground, without signalling that group or displacing a live unrelated job; unclaimed background jobs retain the existing foreground. Keep complete native detached/job-control acceptance as a stable gate.
- Failed descendant exec preparation with a typed native lookup/permission/format/loop classification returns the native error to its caller; it does not imply lost interception. Privileged/protected image admission, archive/runtime errors, final launch revalidation and injection acknowledgement remain whole-run failure boundaries. Preserve native vector/variadic/PATH exec semantics and their nested ZIP/environment/argument conformance.
- Yarn unplugged installation structure retains native sibling lookup and ordinary missing-path errors; traversal into later package-owned dependency namespaces remains virtual and read-only, with genuine physical conflicts rejected without modifying user files.
- The complete requirements remain normative even when a development build has incomplete platform capabilities. Stable readiness must describe evidence truthfully and block stable publication until every gate passes. Experimental next publication requires all four native execution, installed-package, TypeScript, archive and benchmark candidate gates plus exact source-version authorization; remaining full-feature, minimum-OS and intermittent initialization acceptance limits must be disclosed. It preserves npm latest, signed GitHub prerelease identity, immutable retry protection and the closed stable gate, and excludes Homebrew. All five pnport npm latest tags now point to stable 0.1.0. The temporary first-name bootstrap exception is removed: preview inspection rejects every non-stable latest, including `0.0.0-stage`, and preserves stable latest plus the immutable next channel. Do not delete historical placeholder versions or mutate published package bytes.
- Offline benchmark tooling repeats identical compiler and filesystem workloads with separate cold/warm cache conditions, source/artifact identities, sampled process-tree memory and inode-aware cache disk measurements. Keep development observations distinct from reviewed release-build benchmark acceptance; publish validation records through PRs/issues/CI artifacts rather than repository evidence documents.
- Official native TypeScript conformance must distinguish two peer providers while sharing the same ZIP-backed consumer declarations. Preserve data-only graph inspection, correct and deliberately incorrect peer bindings, and unchanged compiler digests in inline/split execution; fixture success alone does not establish complete release acceptance.
- The native filesystem view does not activate the Node PnP API. Libraries requiring `pnpapi` need caller-selected Yarn loader activation; retain the explicit and inherited activation controls in both conformance formats under the [Rust runtime boundary](crates-pnport-foundation.md#node-pnp-api-boundary).

The current source provides a tested development foundation, configured distribution tooling, and public guides. Shared direct-lookup classification now also drives virtual dependency entries in macOS and Linux parent enumeration, at registered package roots, including nested dependency aliases and peer contexts. Ordinary source/output descendants retain native lookup, enumeration and cleanup; native resolvers ascend to package roots. Per-stream/shared-offset lifecycle handling does not replace the outstanding platform and tool acceptance gates. Its macOS preload synchronizes concurrent fork state, propagates injection through virtual-PATH `posix_spawnp` launches, and makes pathname and descriptor-relative link reads share logical target and native buffer semantics. Its macOS supervisor uses an authenticated guardian outside its anchored command group for native birth-bound signal forwarding and authenticated abrupt-owner recovery, including stopped additional groups/sessions, hands off the caller's controlling terminal for ordinary foreground/background stop and resume, and retains detached-tree ownership and broader terminal job-control acceptance as release gates. Its Linux owned-child syscall backend runs dynamic and static ELF children; Ubuntu 22.04 arm64 Docker execution passes native C, static Go, and official TypeScript inline/split fixtures. The arm64 host's amd64 container emulation cannot perform the required child tracing and never substitutes for native x64 CI acceptance. [The Rust evidence section](crates-pnport-foundation.md#current-implementation-evidence-and-remaining-gates) records the remaining runtime acceptance work. The separate exact-tag workflow validates four-host candidates. Stable publication requires reviewed exact-version authority and retained final gates under the 2026-10-04 amendment; passing candidate fixtures or preparing a version commit or tag alone does not grant that authority. Full conformance, minimum-OS and benchmark acceptance remain the default prerequisites for later releases. Experimental publication additionally requires the exact reviewed next-version declaration and the complete four-host candidate gates. The experimental `0.1.0-next.1` version is published through npm next and signed GitHub prerelease archives. Stable 0.1.0 is published through npm latest, signed GitHub archives and Homebrew under the 2026-10-04 amendment. The immutable preview remains on npm next and its prerelease archives. Full acceptance and the recorded intermittent failures remain open; publication does not close #958 or grant Windows support.

The macOS adapter preserves command-specific `fcntl` arguments during loader-constructor reentry and early TLS rejection, without runtime bookkeeping on rejected admission. Focused constructor, guard and duplication-provenance controls have native arm64 evidence; native x64 execution remains an acceptance requirement. This correction does not complete broader native-library or Next.js/Turbopack compatibility.

The Linux syscall adapter permits ordinary Node/libuv IPC that reserves optional receive control space. A task-private header prevents descriptor installation; actual ancillary transfers and batched control reception remain unsupported. Preserve this behavior across dynamic and static children under the [Rust IPC contract](crates-pnport-foundation.md#interfaces-and-contracts). Tool-specific execution evidence belongs in PRs and CI artifacts and does not complete the four-host release gate.

The macOS multi-group ownership extension is implemented as defined in the Rust foundation contract: native birth/version admission, authenticated accepted-record recovery, and kernel audit-token signals. Full native acceptance remains a separate stable release gate.

## Change Policy
Update this index, the relevant domain contracts and AGENTS files together. Record implemented behavior and outstanding release gates separately; preserve deferred Windows requirements and keep #958 open until the complete scope is accepted.

## References
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
