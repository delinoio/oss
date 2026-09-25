# React Forge Validation

## Scope
Acceptance evidence and repeatable validation for `packages/react-forge`, its native engines and the shared Forge boundary. The complete requirements remain normative; this document records evidence without expanding platform, performance, Office or accessibility claims.

## Runtime and Language
Parley/Fontique 0.9.0 (CoreText enumeration and macOS 15 CJK fallback fixes), Node.js 24, React 19.2.8, react-reconciler 0.33.0, TypeScript 5.9.3 and pinned Rust nightly-2026-01-01 on macOS/Windows/glibc Linux x64/arm64. Historical local measurements below used Node.js 24.20.0 on macOS arm64. Test scripts use JavaScript and Python; production generation uses only JavaScript and Rust.

## Users and Operators
Repository developers and CI maintainers reproducing document, preservation and session behavior.

## Interfaces and Contracts
- `pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge` builds and validates the real library plus workspace and packed-consumer CLI.
- `pnpm --filter @delino/react-forge test:render --output <directory>` creates four formats, edits three external Office fixtures, renders their originals for comparison, and independently verifies package XML, extracted text, native chart visibility, CJK and PDF semantics.
- `pnpm --filter @delino/react-forge benchmark --output <report.json>` records eleven fresh-process workload samples.
- CI `react-forge` runs on six native platform/architecture runners. Its native/system-font work is uncached and its seven-day artifacts contain Office/PDF files, page PNGs, provenance and benchmark reports.

## Storage
Committed `packages/react-forge/tests/evidence` JSON records contain local observation data and version/font checksums. Rendered artifacts are explicitly requested local output or short-lived CI artifacts; no system fonts are copied or redistributed. Existing external fixture bytes and their reproducible generators remain under their owning native crate tests. Generated package dist is removed from final worktrees.

## Security
Fixtures contain synthetic repository-owned content. Test-only tools do not become production dependencies. Diagnostics and committed evidence contain no credentials, document-user data or local user paths. Caller task execution is trusted and unsandboxed. No package/release publication, telemetry or Microsoft Office automation is part of these checks.

## Logging
Runtime diagnostics are checked for safe classifications and model locations, observer failure isolation and scoped native tracing. Benchmark logs identify format/workload and summary timing/memory. Renderer evidence records tool versions, font file names/version/checksums, raster hashes and structural check results.

## Build and Test
Run from the repository root after `pnpm install`:

```sh
pnpm --filter @delino/react-forge build
pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge
cargo test -p forge-package -p forge-document -p forge-docx -p forge-xlsx -p forge-pdf -p forge-tree-doc -p forge-pptx -p delino-forge
cargo clippy -p react-forge-node -p forge-package -p forge-document -p forge-docx -p forge-xlsx -p forge-pdf --all-targets -- -D warnings
pnpm ci:contracts
pnpm ci:workflows
pnpm --filter devhud verify:pins
pnpm --filter devhud verify:mobile
```

Install LibreOffice and Poppler as test tools and create a Python environment from `packages/react-forge/scripts/render-requirements.txt`. Explicit test tool paths can be supplied through `REACT_FORGE_SOFFICE`, `REACT_FORGE_PDFTOPPM` and `REACT_FORGE_PYTHON`. Then run:

```sh
pnpm --filter @delino/react-forge test:render --output /tmp/react-forge-render
pnpm --filter @delino/react-forge benchmark --output /tmp/react-forge-benchmark.json
FORGE_RENDER_ARTIFACTS=/tmp/forge-render cargo test -p delino-forge --test render -- --ignored
```

The legacy Forge renderer finds `soffice` and `pdftoppm` on PATH. The React Forge harness explicitly configures test-only fontconfig with macOS system-font directories because headless LibreOffice may otherwise omit them.

Root `cargo test` also requires the existing DevHud frontend build prerequisite (`pnpm --filter devhud build:frontend`). On this macOS checkout, repository-wide validation used canonical `TMPDIR=/private/tmp` and serial test execution for process-global legacy CLI fixtures. The existing generic fspy embedded preload and pnport-specific runtime preload must be prepared independently: first run `cargo test --no-run` to embed the generic artifact, then build `fspy_preload_unix --features pnport` in a separate target directory and copy its dylib into the root target's debug and debug/deps runtime locations. Run `TMPDIR=/private/tmp cargo test -- --test-threads=1`. Enabling the pnport feature across the entire workspace instead changes generic fspy interception semantics and is not equivalent. This is test preparation for existing consumers, not a React Forge runtime dependency. Remove all generated repository-owned dist directories after validation.

Coverage includes:

| Boundary | Evidence |
| --- | --- |
| React behavior | Real reconciler tests for composition, state/reducers, memo/Context, refs, effects/Strict Mode, classes/errors, profiler, external stores, Suspense/use/lazy/rejection, transitions/deferred/action state and Activity |
| Session lifecycle | Revision pinning, font revision changes, stale handles, overlapping/in-flight mounts, simultaneous exports, disposal and cancellation |
| Publication | Conflicts, overwrite, original-source changes, concurrent saves, symlink behavior, FIFO rejection, no-clobber races, cleanup and truthful post-publication results |
| CLI | All four workspace and installed tasks, ESM/CommonJS tasks, JSON/human errors, invalid arguments, task failures, SIGINT/SIGTERM and cleanup |
| Office preservation | External PPTX/python-pptx, DOCX/python-docx and XLSX/openpyxl fixtures; unchanged package/XML bytes, repeated external PPTX edits, chart/data replacement, numbering/drawing ID collisions and opaque regions |
| Spreadsheet | Typed cells and formula caches/recalculation, dimensions, merge/freeze/filter/link behavior, three charts, five conditional-format families and seven validation families, unsupported extensions |
| Fonts | System and caller fonts, CJK/RTL/mixed direction/color emoji, missing glyphs and color representations, restricted/no-subsetting/bitmap-only embedding flags |
| PDF | Paragraph/table splitting, repeated-header artifacts, first-header/body placement, overflow, language/alternate text/links and structure-tree page/MCID logical reading order |
| Limits | Input/output/part/expanded bytes, ZIP count, XML depth/nodes/entities, image bytes/pixels, React tree bytes/depth/count, chart/merge expansion |
| Legacy contracts | Existing Forge engines, CLI and official MCP client, optional-renderer test, immutable DevHud desktop/mobile pins and affected CI contracts |

Rendering evidence checks seven generated/edited cases with LibreOffice 26.8.0.3, Poppler and pinned pypdf/lxml/Pillow versions recorded in the committed report. Every edited fixture is compared against its source pagination; the external XLSX's pre-existing blank page and split print chart remain preserved. Native PDF has three pages with repeated table headers and logical structure tags. Word charts show native series data, and its text extraction retains CJK. PNG page images were also visually inspected. This is structural and LibreOffice/Poppler interoperability evidence, not direct Microsoft Office validation or PDF/UA certification.

The benchmark uses the normal development native profile on an Apple M5 Max. Near-limit workloads use 950 PPTX slides, 9,500 Word/PDF paragraphs or 19,000 spreadsheet cells. Peak RSS includes the whole fresh Node process and native/font allocations. System color-emoji font loading makes the representative PDF sample materially larger than a Latin-only document. Event-loop p99/max includes TSX loading, React reconciliation and serialization as well as awaiting native work; native processing does not imply zero JavaScript blocking. Exact observations are in the committed report and vary by machine, fonts and load. There is no latency, memory or throughput SLO.


Measured on 2026-09-24 (development build; observations only):

| Format | Workload | Total ms | Peak RSS MiB | Event-loop p99 ms |
| --- | --- | ---: | ---: | ---: |
| PPTX | representative | 278.1 | 107.1 | 57.1 |
| DOCX | representative | 132.4 | 112.3 | 52.0 |
| XLSX | representative | 136.9 | 108.5 | 84.0 |
| PDF | representative | 190.5 | 595.2 | 53.0 |
| PPTX | preserved edit | 132.8 | 81.4 | 7.8 |
| DOCX | preserved edit | 468.7 | 85.3 | 5.8 |
| XLSX | preserved edit | 79.4 | 81.1 | 8.6 |
| PPTX | near limit | 3088.2 | 135.4 | 6.1 |
| DOCX | near limit | 4225.5 | 219.6 | 5.7 |
| XLSX | near limit | 2298.1 | 261.0 | 5.7 |
| PDF | near limit | 2466.6 | 268.8 | 5.7 |

## Scoped Package Identity

The npm package is `@delino/react-forge`; the project/directory identity and CLI executable remain `react-forge`. The scoped package passed `pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge`, including all 34 tests and an installed consumer of `delino-react-forge-0.0.0.tgz`. That consumer checks the installed manifest name, imports the scoped entry points to generate all four formats, and runs the `react-forge` binary. CI's package selectors use the scoped name; all 73 CI contract tests and workflow validation passed. The ROAM task also typechecks and exports through the scoped workspace package. This naming change does not publish the private package.

## Designed Travel Investor Example

The 12-slide English ROAM deck is generated by `packages/react-forge/examples/travel-ir.tsx`; its assumptions, market source, original generated imagery, exact image prompts and reproduction commands are documented in `examples/travel-ir-assets/README.md`. The example adds no runtime converter or network dependency. All company-specific figures and product visuals are explicitly illustrative.

Validation on 2026-09-24 passed the example's standalone TypeScript check and the existing 34 package tests. The delivered PPTX is byte-identical to the Forge CLI export. Package/geometry/font-policy checks, native table ownership on slide 7, native chart ownership on slide 8, the embedded workbook and matching `[500, 1500, 3000, 5000]` chart caches passed. Independent calculations checked the $15.36M cohort scenario, 10,000 first-year booking target, $640K revenue target and $1.5M allocation total. No external package relationships were present.

The deck imported successfully into Artifact Tool without rewriting it, and test-only bundled LibreOfficeDev 26.8.0.0.alpha0 with Poppler 26.05.0 rendered all 12 slides at 1440 × 810. All 144 native slide-text runs survived PDF text extraction; every rendered slide was visually reviewed after final layout adjustments. Avenir Next is referenced for slide text. The native chart references Calibri, which this renderer substituted with Carlito; both the referenced and rendered font families are recorded. No fonts are redistributed and no direct Microsoft PowerPoint or Google Slides execution is claimed.

The compact evidence record is `packages/react-forge/tests/evidence/travel-ir-macos-arm64.json`. The delivered export's SHA-256 identifies the reviewed artifact; subsequent exports can differ because new document/node IDs are UUID v7. PPTX/PDF/PNG outputs and tool-owned validation receipts stay local and untracked.

## Dependencies and Integrations
Foundation PR #967 at `303fa6747d89fae427725b6f50e6a1a58b10bafb` is the reused repository implementation. Test fixtures document their generating tool versions beside their sources. CI installs optional renderers only on its disposable test host; the private package never downloads tools or fonts.

## Change Triggers
Update evidence and relevant project/native/Node contracts when formats, preservation, runtime compatibility, fonts, lifecycle, limits or validation commands change. Keep central CI path selection, required result aggregation and owning AGENTS rules synchronized.

## References
- [Project](project-react-forge.md).
- [Complete requirements](packages-react-forge-requirements.md).
- [Node contract](packages-react-forge-contract.md).
- [Native contract](crates-react-forge-contract.md).
- [Repository defaults](repository-defaults.md).
- [Workflow contract](repository-workflow-contract.md).

## Cross-platform extension
The 2026-09-24 PR #970 follow-up expands the initial macOS-arm64 observations above to six native hosts. The CI matrix runs build, native and React regressions, installed consumers, system-font tests, ROAM example generation and benchmarks on every host, plus isolated Windows console cancellation and macOS/Linux render checks. Each Rust invocation selects the matching target explicitly, including Windows arm64, and Node asserts its actual host architecture. The initial macOS evidence records remain historical observations rather than proof for another operating system.

| Native ID | Runner | Native target |
| --- | --- | --- |
| `darwin-x64` | `macos-15-intel` | `x86_64-apple-darwin` |
| `darwin-arm64` | `macos-15` | `aarch64-apple-darwin` |
| `linux-x64-gnu` | `ubuntu-22.04` | `x86_64-unknown-linux-gnu` |
| `linux-arm64-gnu` | `ubuntu-22.04-arm` | `aarch64-unknown-linux-gnu` |
| `win32-x64-msvc` | `windows-2022` | `x86_64-pc-windows-msvc` |
| `win32-arm64-msvc` | `windows-11-arm` | `aarch64-pc-windows-msvc` |

The [initial matrix](https://github.com/delinoio/oss/actions/runs/35992992719) at `fba6059d` passed both macOS and Windows architectures and exposed Linux's monochrome emoji fallback failure. After the color-first grapheme repair, both Linux jobs, both Windows jobs and macOS arm64 passed at `6fa32003` in the [runtime validation run](https://github.com/delinoio/oss/actions/runs/35994740028). Final follow-up results, including the complete investor example on every host, are recorded in [PR #970's checks](https://github.com/delinoio/oss/pull/970/checks) and PR description. These are native executions, not cross-compilation-only evidence.

Local follow-up verification passed root `cargo test` (1,883 passed; three existing ignored tests), targeted native Clippy with warnings denied, 45 package tests, typecheck/lint, 73 CI contract tests and workflow validation. Seven generated/edited LibreOffice/Poppler cases passed after the font repair. macOS source protection additionally covers removed case and Unicode-normalization aliases.

The investor example selects Avenir Next/Segoe UI/Noto Sans by host, keeps body frames intact with bounded shrink, and accommodates two-line table cells. A caller-only DejaVu Sans probe reproducing Linux's wider fallback now exports successfully. The refreshed macOS deck retains all 144 extracted native text runs, its 12 slides, native table/chart/workbook and original text sizes. Raster comparison leaves 11 slides byte-for-byte visually unchanged; only slide 7's increased table row height differs, and that slide was visually checked. The updated artifact's SHA-256 is `30e1cebee59cfaa6e0702814d1003d6f9c7744186e1ab6b723ded36ea902f878`. Package integrity, geometry/font policy and independent Artifact Tool import passed without rewriting the Forge export. This does not claim identical font metrics or Office rendering across platforms.

## Figma creation and preservation validation

The remote Figma extension is independently covered by `forge-figma` planner tests and fake-canvas/official-SDK transport tests. The latter exercise synthetic Codex/Claude credential records, expiry/401, capability/code limits, shared admission and file queues, Retry-After and quota exhaustion, three safe retries, ambiguous writes, partial setters on new and existing nodes, cancellation, React state, native resources, image-hash reuse, SHA-256 vectors, paginated/deduplicated reads, mounted refs, foreign-child protection and unchanged publication. Installed-archive CLI coverage now includes an offline `.figma.json` task. No automated test reads live Keychain or connects to Figma.

Live acceptance on 2026-09-24 used the selected Pro plan's Drafts and Codex's existing MCP authorization. [ROAM — Travel Companion](https://www.figma.com/design/ccKXvoUvorNx58j9YLs5iC) contains five 390×844 mobile screens: Explore, Search, Destination, Itinerary and Saved. Final structure is 207 scene nodes: 103 text, 67 frames, 32 rectangles, three components, one component set and one instance, with seven image fills. One collection contains seven color variables, and a native text style is present. Fonts use available DM Sans Regular/Medium/Bold; Avenir Next from the existing investor sample was unavailable. Photography is reused original concept imagery, not a claim of authentic destination photography.

The creation session was closed. Reopening changed the Explore title and image, the itinerary cover position/list spacing and first stop, and added a sunset note. A subsequent reopened run retained all 209 existing inspected identities (including two pages) and reused that note. Search, Destination and Saved retained identical fingerprints for all 113 inspected nodes. An unchanged publish performed zero additional tool calls and created no duplicates. Final screenshots of all five screens passed visual inspection after correcting clipped card detail text and a wrapped Save label.

[The external fixture](https://www.figma.com/design/wyvfmaofzMTzG5m8UoXLX7) was authored directly with official Figma tools without Forge metadata. React Forge changed its existing frame and text IDs, created no nodes, and preserved exact fingerprints of four unselected vector/group/ellipse/text nodes.

The compact committed record is `packages/react-forge/tests/evidence/travel-figma-macos-arm64.json`; it contains node links, screenshot hashes, structure and preservation results, and per-session call/retry/wait totals. Named successful sessions used two tool calls for blank-file creation, 22 for generation completion, ten for visual corrections, 19 for the reopened-edit validation (including full-page reads before and after), and five for the external edit. These exclude SDK negotiation/listing and separate agent inspection. Interoperability discovery encountered actual comma-bearing style IDs, MCP text truncation near 20 KiB and the `submitUrl` upload field. Scratch content was cleaned or the recorded model was explicitly reconciled by the developer; ambiguous creation was not automatically replayed. Generation completion therefore includes recovery of that in-progress model. Final create/update behavior is also exercised from clean state by the executable canvas and installed CLI fixtures.

Validation commands include root `TMPDIR=/private/tmp cargo test -- --test-threads=1`, planner/native Clippy, `pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge`, standalone TypeScript checks for both Figma examples, and repository CI contract/workflow checks. Existing Office/PDF generation, editing and installed-consumer regressions remain in the package suite. The existing optional renderer test remains separately gated; this change does not alter the local document engines.

After rebasing onto PR #970 at `70efa714`, macOS arm64 verification passed 1,906 root Rust tests (three existing opt-in tests ignored), all 73 package tests including installed consumers, build/typecheck/lint, planner/native Clippy, both Figma example type checks, 73 CI contract tests and workflow validation. Permanent HTTP request failures also have explicit no-retry coverage. The broader native-host matrix remains CI evidence, not a claim of local execution on other operating systems.

## PR review repair validation

The subsequent one-shot repair of PR #970 addresses 18 actionable review threads and the independent Linux pnport cleanup failure in 19 separate repair commits. Regression coverage includes unsupported presentation semantics, opaque descendant deletion/movement, imported shape edits, source-bound updates whose imported text exceeds the 16 MiB authored-tree envelope, typed Word breaks, conservative chart ownership, cell style inheritance, independent list instances, source section/cell measurement widths, explicit non-bold PDF headings, OPC namespace authority, the accepted XML depth boundary, Excel General alignment, differential false overrides and operation-pinned diagnostics.

Final local macOS arm64 verification at the repaired implementation (`140974d6`) passed root `cargo test` with 1,910 passed and three existing ignored tests, package build/typecheck/lint with all 50 React/CLI tests, and targeted native Clippy with warnings denied. All seven generated/edited LibreOffice/Poppler cases passed, the separately enabled legacy Forge renderer test passed, and the full 12-slide ROAM example exported again. The unchanged CI contract/workflow checks also passed (73 contract tests). Generated `dist` trees were removed after validation.

The Linux repair leaves a denied syscall parked until cleanup queues its termination signal, avoiding the short-child exit race. Offline native Ubuntu 22.04 arm64 Docker passed all 64 pnport tests and Clippy; the deterministic stopped-child regression fails with the old premature continuation and passes with the repair. This is native arm64 evidence, not native x64 execution.

All six React Forge native jobs passed at `70efa714` in the [pre-repair matrix](https://github.com/delinoio/oss/actions/runs/35996920474). That result covers the six-platform implementation before these review repairs. CI is rerun by the single final repair push; this local record does not claim a result for that newer remote head. Microsoft Office execution and PDF/UA certification remain outside the evidence boundary.


## Figma PR review repair validation

The one-shot repair of PR #972 merges parent #970 at `653e1842` while preserving both validation histories. Five separate fixes cover indirectly changed collection/variant guards, a session-wide credential reread allowance, native rejection of nested pages, retained ownership transfer on remount, and serialized aggregate image admission. Regressions reproduce the collection self-conflict, remount overlap and concurrent 256 MiB budget bypass; additional cases verify no credential rereads after rejection or successful recovery, rejection before page/file creation, retained descendant IDs, duplicate image capacity and recovery after cancelled/failed registrations. All Figma tests remain synthetic and do not access live credentials or files.

Final macOS arm64 verification passed root `cargo test` with 1,922 passed and three existing opt-in tests ignored, all 85 package tests including installed consumers, build/typecheck/lint, native planner/adapter Clippy with warnings denied, both Figma example type checks, 73 CI contract tests and workflow validation. The pre-push PR head had no failing CI checks; this record does not claim results for the final repair push. Generated repository `dist` trees are removed after verification.

## Session-based MCP extension

The local MCP follow-up adds nine stdio tools, a shared TSX execution process and settled local snapshots. Local Node.js 24.20.0 on macOS arm64 passed the package build, typecheck, lint and standalone example checks. The package suite passed 100 tests, including 15 new MCP protocol/session/lifecycle cases and the expanded installed private-archive test. Repository CI contracts passed 73 tests, and workflow validation passed.

The real SDK client exercised all four local formats and fake Figma through the built and installed executable. Evidence covers inline and entry-relative TSX, canonical module identity, React hooks/Suspense, retained Office mounts and original-file protection, revision-aware inspection/measurement, source limits, output/diagnostic isolation, queued cancellation, late-return disposal, EOF/signals, blocked-event-loop shutdown, worker loss, and complete/partial/unknown Figma receipts without blind retries. Installed consumers also generated each local format and a fake Figma receipt through MCP. No live Figma publication or Keychain access was used by this validation.

The existing six-host job now runs the MCP tests through the package test command and checks standalone examples after building. At this local-validation point, hosted Windows/Linux and additional macOS architecture results remained pending; the later public-release matrix passed on all six hosts, as recorded below. This extension changed no Rust code or native engine defaults. The existing native binding was rebuilt for package integration, and generated `dist` is removed after validation.

## Public npm candidate validation

The public package consists of the main library and six exact-version native optional packages. Seven external `0.0.1` packages reserved the npm names without runtime code. Local `package:main`, `package:native` and `test:package` check the installed candidate on the current host; PR CI checks installed consumers on each host. The exact-tag workflow combines all six native tarballs and repeats six-host build, installed-consumer and complete-set gates before OIDC publication. The `0.1.0` tag passed all six release hosts and the seven-package assembly on its second attempt, after one Linux x64 MCP fixture readiness timeout. Its publish job then failed before any registry write because the registry lookup callback received the array index as its request argument. The corrected `0.1.1` [release run](https://github.com/delinoio/oss/actions/runs/36048311720) passed all six hosts and published all seven packages. Downloaded candidate tarballs matched npm's SHA-512 integrity for every package; npm metadata reported SLSA provenance and `latest: 0.1.1` for each. See [release contract](packages-react-forge-release-contract.md).

## Static GLB/FBX acceptance (2026-09-25)

The generation-only scene extension was exercised locally on macOS 26.6.2 arm64 with Node 24.17.0, React 19.2.8, Rust 1.94.0-nightly (`8d670b93d`, pinned nightly-2026-01-01), Khronos gltf-validator 2.0.0-dev.3.10, ufbx 0.23.0 (Rust crate 0.11.4), Three.js 0.186.1 and Blender 4.5.14 LTS (`62c1db4208e8`). Blender's official macOS arm64 DMG SHA-256 was verified as `65134d9b07b20e2fa8d3c9e44f6f44ffb5c9774dd521b95f50387310241ca170`. Blender is mounted separately as a test tool; it is never part of a production generation path.

The original AURA headphones, desktop DAC/amplifier, stand and combined studio produce eight product files. Each GLB passed the Khronos validator with zero errors. Each FBX passed independent ufbx parsing with embedded-texture, normal and meter-unit checks. Separate empty-scene Blender imports verified mesh counts, single UV/material bindings, hierarchy, cameras, lights and world bounds, with maximum world-bound error below 0.00001 meters. Unnamed nodes receive different Blender-generated labels across importers; the comparison normalizes only those invented names and checks the authored hierarchy without modifying either imported scene. A separate profile fixture verifies mirrored/nonuniform parent transforms using bidirectional world-vertex comparison, opacity, both camera projections and all three light types. Intermediate mesh/root matrices differ because importers place the axis conversion at different hierarchy levels; this is not a geometry discrepancy.

All eight product files were rendered at 2048×2048 with Cycles, 96 samples, fixed seed, AgX, and identical fixture lighting/exposure on an explicitly selected Metal device. Front, back, oblique and detail views were inspected directly. Front views use an orthographic camera; other views use perspective. Imported meshes and materials were never repaired or replaced. A local loopback Three.js viewer loaded all four actual GLBs; interactive rotation/zoom and console checks passed. Browser build information was unavailable through the browser tool; the viewer's exact library version and screenshots are retained.

Visual work corrected FBX enum flags rejected by Blender's camera reader, the FBX front-axis sign, baked FBX texture factors, rounded-surface UV spacing, headband end caps, the suspension bridge and stand contact height. Test-studio light levels and camera framing were also corrected after reviewing overexposure and background horizon/near-origin artifacts. GLB/FBX pixel differences are observational evidence rather than an automatic visual pass criterion. Closed product surfaces look consistent in the reviewed images; Blender 4.5 ignores FBX culling flags and shades both sides, which remains a documented importer limitation.

Local verification passed root `TMPDIR=/private/tmp cargo test -- --test-threads=1` with **1,927 passed and three pre-existing opt-in tests ignored**, targeted scene/exporter/adapter Clippy with warnings denied, package build/typecheck/lint with **112 tests**, standalone example checks, public main/native candidate assembly and installed six-format CLI smoke, installed-archive six-format MCP inspection/measurement/export, **77 CI contract tests**, workflow validation, and `pnpm test` from `apps/public-docs` (all sixteen React Forge guide routes). Root test preparation followed the existing generic/pnport preload separation above. Earlier root attempts exposed existing path-alias/preload preparation requirements and transient clibox process-fixture failures; the final complete serial run passed. An initial Node 24.11.0 MCP loader issue was avoided by validating with Node 24.17.0; this does not establish compatibility for every Node 24 patch.

The six-host native CI matrix includes the three scene crates, and Linux x64 additionally installs checksum-pinned Blender 4.5.14 and renders the fixtures. **Only macOS arm64 was executed locally for this change. Other host results and the new Linux rendering job have not been observed and are not marked passed.** GLB/FBX remain unreleased, absent from the historical npm 0.1.1 release.

Reproduce after building:

```sh
REACT_FORGE_BLENDER=/path/to/blender pnpm --filter @delino/react-forge test:scenes --output /tmp/aura
# Optional macOS acceleration: append --device METAL.
python packages/react-forge/scripts/compare-scenes.py /tmp/aura
node packages/react-forge/scripts/scene-viewer.mjs --input /tmp/aura
```

`test:scenes` writes product/profile exports, Khronos results, ufbx summaries, camera/light/hierarchy checks, and render hashes. Pillow from `render-requirements.txt` produces comparison contact sheets. The compact committed record is `packages/react-forge/tests/evidence/audio-studio-macos-arm64.json`; generated models, full reports, 32 renders and web screenshots are delivered separately. Remove repository-owned `dist` after validation.

### AURA presentation quality revision

The subsequent quality revision preserves the original acceptance record as a historical baseline. It replaces the simplified product geometry with vertically oval machined earcups, displaced leather pads, actual tube stitching, supported adjustment rails and pivot hardware, integral encoder flutes, a closed perforated lid with 36 beveled through-slots, recessed dust screens, differentiated rear connectors and engraved identifiers. Seven deterministic texture sources include 2048px cellular leather and brushed metal maps, a woven liner normal, an OLED display and an original antialiased vector-glyph atlas. No generated image substitutes for an exported mesh, and no external font or art asset is required.

The high-quality verification profile is `test:scenes --output <directory> --device METAL --samples 256 --hero-resolution 4096`. Individual heroes are 4096×4096, the studio hero is 4096×2731, and front/back/detail images are 2048×2048. The regular studio hero keeps a minimum 2048px short edge even without the high-resolution option. Framing fits independently imported bounds with a margin; intentional detail crops are excluded from complete-product framing. Each render records its own dimensions and hash. Key, fill, overhead and edge softboxes are identical for both importers. The web fixture adds only environment lighting, shadows and a floor, keeping imported mesh/material data unchanged.

Procedural geometry regression checks validate finite attributes, outward triangle winding and orthonormal tangent frames, including thin boxes, rounded caps, leather displacement, bent tubes, stitching, fluted grips and perforated metal. Close-up review exposed angular UV stretching on the woven acoustic liner; planar cap UVs and their matching tangent frames correct that issue in the authored geometry before export. Record this revision's executed results separately from the initial engine acceptance; do not re-label historical Rust/platform checks as fresh executions.

The loopback gallery can show the actual imported GLB/FBX hero, rear and detail renders, with explicit format and pixel dimensions, or switch to interactive GLB inspection. Its server accepts only fixed product/render filenames under the selected artifact directory. Missing renders remain an explicit unavailable state. GPU rendering pauses while the gallery image is visible; mode switches retain the selected product.

Final local macOS arm64 acceptance of this revision passed eight product imports and all 32 rendered views using the 256-sample/4K-hero profile above. All four product GLBs have zero Khronos errors and warnings; independent ufbx parsing and the Blender structure/profile checks passed. The largest product world-bound error is below 0.000000017 meters. Direct inspection of the four format-paired contact sheets and full-size material details found no missing textures, broken surfaces or unintended full-product clipping. The largest paired mean absolute RGB difference is 0.09294 on a 0–255 scale; this is observational evidence, not a visual pass threshold. All four final GLBs were rotated and zoomed in the local viewer, and GLB/FBX gallery switching passed with zero fresh console warnings or errors.

The package build/typecheck/lint, **113 package tests** including installed six-format CLI/MCP checks, and standalone example type checks passed after the planar-UV correction. Regenerating all seven source textures with Node 24.17.0 produced identical bytes. The separate committed record is [audio-studio-quality-macos-arm64.json](../packages/react-forge/tests/evidence/audio-studio-quality-macos-arm64.json), containing source, model and image hashes, versions, checks and visual observations. Model/render archives and a labeled before/after presentation comparison are delivered outside the repository. Rust sources and public guides were unchanged in this refinement, so their earlier checks remain historical; other hosts and hosted CI were not executed or marked passed.

### PR #988 render scheduling repair

The first hosted run (`36115873712`) completed native/package checks on Linux x64 but reached the 60-minute job limit during sequential CPU scene rendering. The studio GLB alone took approximately 38 minutes for four views, leaving only five of 32 images complete when the job was cancelled. Preparation now remains in the native job, while four required product jobs consume its validated exports and render both formats independently with 120-minute budgets. Resolution, 96-sample CPU quality, scene geometry/materials and all 32 views remain unchanged. The comparison stage validates the complete requested product set and input/image hashes before producing evidence. The same run’s unrelated DevHud API OCI build failed on a Go module proxy connection reset; no application failure was reported, and the final repair push retries that job without changing its source. New hosted results remain pending.

Local repair verification passed 78 CI contract tests, workflow/actionlint validation, six Python evidence regressions, package build/typecheck/lint with 113 tests, and standalone example checks. Fresh preparation generated and independently inspected all eight models, then a separate Blender 4.5.14 process rendered the stand's GLB/FBX pair at 2048px/96 samples on macOS Metal. All eight images and their contact sheet passed comparison and visual inspection. The new verifier also accepted the complete previous 32-image record without rewriting it. This verifies the preparation-to-render boundary locally; it is not a hosted Linux CPU timing result. Rust and frontend sources were unchanged, and their broader historical checks were not rerun.

## Sprite extension validation (2026-09-25)

Local validation uses macOS arm64, Node.js 24.20.0 and the repository Rust nightly. Package build, typecheck, lint and standalone examples passed. `pnpm test` passed 110 tests, including seven sprite cases and installed workspace-archive CLI/MCP generation. The affected CI contract, planner and release contract suites passed 37 tests. The existing Forge engine regression selection passed 126 tests with one existing optional renderer test ignored. Native sprite/adapter Clippy passed with warnings denied. The final focused native sprite suite passed eight tests, including the deepest empty layer and a source-image crop wider than the logical canvas limit.

The source implementation is committed at `4116283a`. Public main/native candidate assembly and `test:package` passed for darwin-arm64, generating all five local formats from an installed package. The compact fixture record is `packages/react-forge/tests/evidence/sprite-macos-arm64.json`.

Sprite coverage decodes PNG pixels to verify alpha compositing, clipping, layer order, palettes, source-image cropping/flips, nearest-neighbor scaling and transparent atlas padding. It also checks deterministic archives, frame order/duration/pivots/loop flags, refs and geometry, React updates, revision-pinned exports, output conflicts, cancellation and recovery, strict props/nesting, asset ownership and resource boundaries. The example produced eight 128×128 frames and a 544×272 sheet, with idle and non-looping hop animations. The sheet was visually inspected; generated PNGs/ZIPs remain outside the repository. No engine-specific importer or external artwork-generation service was exercised.

The shell initially selected Node.js 24.11.0. Its synchronous loader failed both the new sprite MCP case and an unchanged existing PDF MCP case with `ERR_INVALID_RETURN_PROPERTY_VALUE`; the already-installed Node.js 24.20.0 passed the full suite. No loader workaround or runtime pin change was introduced. Initial root `cargo test` runs reached five binpm CLI failures involving macOS `/var` versus `/private/var` temporary paths; canonical `TMPDIR=/private/tmp` fixed those cases. That root run then identified missing pnport injection artifacts; preparation follows `docs/crates-pnport-foundation.md` (`cargo build -p pnport -p fspy_preload_unix --features fspy_preload_unix/pnport`). Cross-platform CI and npm publication are not claimed by this local evidence.

After that documented preparation, root `TMPDIR=/private/tmp cargo test` passed **1,930 tests**, with three existing opt-in tests ignored. The final package suite again passed **110 tests** after the last native rebuild, and the installed public candidate reported five generated local formats. No binpm/pnport production code was changed. Generated `packages/react-forge/dist` (including local candidate archives) was removed after validation; the inspected example output remains outside the repository.

### PR #989 CLI readiness repair (2026-09-25)

The [darwin-x64 CI job](https://github.com/delinoio/oss/actions/runs/36124614014/job/108037799956) passed 109 package tests and failed the Unix CLI signal fixture before sending a signal: its mounted-effect readiness marker did not appear within the approximately two-second polling window. A 2.1-second task-start delay reproduces the same assertion locally. The repaired test waits for actual mounted readiness under a 30-second monotonic deadline, stops on an early process exit, and retains the delayed-start regression. SIGINT/SIGTERM exit codes, redacted cancellation, effect cleanup and absence of partial exports remain asserted.

On macOS arm64 with Node.js 24.20.0, all three focused CLI tests and all 110 package tests passed after the native rebuild. Package typecheck, lint and standalone example checks also passed. This is local verification of the CI root cause; the repaired darwin-x64 job requires a new CI run.

The accompanying sprite review repairs passed 11 native tests and sprite/adapter Clippy with warnings denied: hidden descendants retain translated geometry, frame and atlas work share one budget, and PNG cancellation interrupts compression within a wide scanline before a subsequent successful export. With the documented pnport preparation, root `TMPDIR=/private/tmp cargo test` passed 1,932 tests with three existing opt-in tests ignored. The regenerated slime archive preserves all nine PNGs' decoded RGBA pixels and the metadata byte-for-byte against the previously inspected artifact; bounded compression changes the encoded archive size to 25,793 bytes. The new sheet was visually inspected. The earlier committed fixture record remains evidence for its named implementation commit, and generated `dist` output was removed after this verification.

## Procedural game SFX extension

The SFX follow-up adds the independent `forge-sfx` engine and `Format.Wav` without
changing Office/PDF engines or Figma publication. Local macOS arm64 verification
used Node.js 24.20.0. Package build, typecheck, lint and standalone example checks
passed, as did all 108 package tests. The new coverage exercises waveform timing,
real React state and refs, invalid latest renders, native diagnostics, output
preservation, cancellation, and the gunshot's attack/tail/silence. Workspace and
installed-archive CLI and real SDK MCP clients generate WAV. The public main plus
host-native candidate also passed `test:package` with five local output formats.
No npm publication occurred; released `0.1.1` does not contain SFX.

Five native synthesis tests passed, checking RIFF fields, PCM size, reference-tone
frequency by zero crossings at both sample rates, seeded noise, left/right pan,
peak attenuation, silence, model limits and cancellation. Native Clippy passed
with warnings denied. Public-docs tests passed with the fifteenth React Forge
route, and all 77 repository CI contracts plus workflow validation passed.
The six-host jobs include the new native crate and installed SFX consumer checks;
those other hosts were not executed locally.

`examples/zombie-gunshot.tsx` was exported by the built one-shot CLI and decoded
independently with Python's standard-library `wave` module. The compact record is
`tests/evidence/zombie-gunshot-macos-arm64.json` under the package. The file has
36,000 mono frames at 48 kHz/16-bit PCM (0.75 seconds, 72,044 bytes), SHA-256
`fa750e6d70bc68f7a10fc327631355bc236e36a18cdb8d6a496b2bb0dc858198`.
Peak is approximately -0.446 dBFS with no clipped samples. First and last samples
are zero; the final 100 ms are silent. First-50-ms RMS is 0.24286 and the
300–550-ms tail RMS is 0.00226. Repeated output matches, and changing the seed
changes the PCM. This validates synthesis and file properties, not subjective
listening quality or in-game acoustic realism. The playable artifact stays
outside the repository, and no third-party recordings are used.

The initial Node.js 24.11.0 run exposed the existing synchronous MCP loader's
`ERR_INVALID_RETURN_PROPERTY_VALUE` even in a React-only task before importing
React Forge. Repeating with
24.20.0, the runtime used in earlier MCP evidence, passed the full package suite.
The first root Cargo run under 24.11.0 stalled in the unrelated clibox npm-signal
fixture; its orphaned test child was cleaned up. The 24.20.0 serial root rerun
passed that fixture without changing clibox or the MCP loader behavior.

The final root run passed 1,927 Rust tests with 3 existing opt-in tests
ignored, using `TMPDIR=/private/tmp fnm exec --using 24.20.0 cargo test --no-fail-fast -- --test-threads=1`. Before that run, the documented macOS pnport
injection companion was explicitly generated with
`cargo build --locked -p pnport -p fspy_preload_unix --features fspy_preload_unix/pnport`;
the previous run had stopped on its missing native artifact. No unrelated Rust
source was changed. Generated repository-owned `dist` directories were removed
after validation.

### PR #987 CLI readiness repair

The initial six-host CI run passed five React Forge hosts. The darwin-x64 job
failed the CLI signal fixture before sending a signal: its fixed 200 polls at
10 ms exhausted the startup budget before the React effect's readiness marker
appeared. All SFX cases passed in that job. The fixture now uses a bounded
30-second monotonic readiness deadline, stops early on process exit or signal,
and includes exit state and captured output in readiness failures. Cancellation
codes, effect cleanup and absence of partial exports remain required.

A deliberate 2.5-second task startup delay reproduced the old assertion failure
locally and passed after the repair for both SIGINT and SIGTERM. On macOS arm64
with Node.js 24.20.0, the focused regression and all 108 package tests passed,
along with build, typecheck, lint, standalone example checks and the public
main/native installed-package smoke test for all five local output formats.
No production code or public contract changed; native Intel Mac execution of the
repair remains a CI validation step.


## PR #988 merge and review repair (2026-09-25)

Merged `origin/main` at `bcc35d49` without rebasing, preserving both the static
GLB/FBX extension and the incoming WAV/SFX extension. The combined installation
suite covers seven local formats, and the public guide inventory contains all
seventeen routes.

Two actionable review findings were repaired independently: native scene
measurement events and errors now retain the layout stage, while document
inspection and generation keep import/export; the app instruction now requires
validation of the complete seventeen-route inventory. Regression tests cover
successful and failed GLB/FBX measurements, exports, and failed native inspections
across all seven formats. The old diagnostic mapping fails these regressions.

The independent macOS x64 CI failure was a cancellation-fixture readiness race:
a marker file could become visible before its write continuation installed the
abort listener. Both fixtures now retain cancellation before publishing readiness
and handle already-aborted signals. The late-return test deliberately continues
after cancellation has arrived. The old late subscription fails deterministically
at the disposal marker; the repaired fixture and all twelve MCP tests pass.
Neither runtime cancellation semantics nor timeout budgets were changed.

Final local macOS arm64 verification of implementation `8b18fce3`, using Node.js
24.17.0 and the pinned Rust toolchain, passed:

- Root `TMPDIR=/private/tmp cargo test --locked -- --test-threads=1`: **1,933 passed,
  zero failed, three existing opt-in tests ignored**, after the documented frontend
  and separate generic/pnport preload preparation.
- Scene, GLB, FBX, SFX and native-adapter Clippy with warnings denied.
- Package build/typecheck/lint and **121 tests**, including installed-archive
  seven-format CLI/MCP coverage, plus standalone example type checks.
- Public main/native candidate assembly and installed seven-format CLI smoke.
- **78 CI contract tests**, workflow validation, and **six scene-comparison tests**.
- `pnpm test` from `apps/public-docs`, including all **17** React Forge routes;
  the rendered inventory also matches the corrected app validation instruction.

The existing 4K product models and comparison renders remain unchanged; this
repair does not claim a new visual render or execution on another host. Final
push CI results are separate evidence. Generated repository-owned `dist`
directories are removed after verification.


## AURA source texture Git LFS migration (2026-09-25)

PR #988 stores all seven AURA source PNGs in Git LFS through the exact
`packages/react-forge/examples/audio-studio-assets/*.png` attribute pattern.
Their combined original size is **10,760,286 bytes**; their seven Git pointers
occupy **916 bytes**. The three source assets above 1 MiB were included, and no
ordinary blob above 1 MiB remains in the PR-only history relative to `main`.

The migration rewrote fifteen PR commits and preserved the base branch. All
non-texture trees were compared against the pre-migration commits; only LFS
pointers and their attribute declarations changed. Across those commits, **94
texture entries** match their original SHA-256 and sizes, representing **12 unique
historical LFS objects** (11,588,830 payload bytes). The original commit IDs in
older acceptance records remain historical evidence; they are not rewritten to
imply the earlier runs used the new storage layout.

Git LFS 3.7.1 uploaded all twelve objects. A separate sparse checkout with an
initially empty LFS cache downloaded the seven current textures from GitHub,
verified their original hashes and sizes, and passed `git lfs fsck`. Using that
checkout's downloaded textures with the validated local dependency/native build,
the example generator produced all eight actual GLB/FBX files. All four GLBs had
zero Khronos Validator errors. This is export and storage verification, not a new
Blender visual render; texture pixels and prior visual evidence are unchanged.

Local macOS arm64/Node.js 24.17.0 verification also passed package
build/typecheck/lint with **121 tests**, installed-archive CLI/MCP coverage,
standalone example type checks, **79 CI contract tests**, and workflow validation.
Source-consuming CI and release build checkouts now enable LFS, and attribute
changes select both native/package validation and scene-render jobs. Generated
repository-owned `dist` directories were removed after these checks.


## PR #988 shared LFS policy merge repair (2026-09-25)

Merged `main` at `13391e4a` (PR #990) without rebasing. The resolved attribute file
retains all thirteen LFS assets, including every AURA texture, using explicit file
paths under the shared 512 KiB policy. The four smaller AURA companion textures
remain tracked with their set. Removed a duplicate release-checkout `lfs` key
introduced by the automatic merge, and extended the shared pointer inventory to
cover AURA and the GLB/FBX asset extensions. Attribute changes retain the shared
planner's selection of every event-eligible job.

All thirteen hydrated assets matched the SHA-256 and size in their merged LFS
pointers, and `git lfs fsck` passed. macOS arm64/Node.js 24.17.0 verification passed
**81 CI contract tests**, workflow validation, the DevHud R2 tests (including its
embedded PNG check), package build/typecheck/lint with **121 tests**, installed
CLI/MCP coverage, and standalone example type checks.

The broader release suite passed 246 of 248 tests on macOS; two Linux packaging
tests could not run there because `dpkg-deb` and GNU tar were unavailable. Both
affected suites then passed all four tests in an isolated Linux arm64
`node:24-bookworm` container (Node.js 24.21.0), using the pinned pnpm 10.26.2 and
clibox 0.1.6 tools. The repository mount was read-only and the test/tool copies
were temporary. No source change was needed for those environment limitations.
Generated repository-owned `dist` directories were removed after validation.


## PR #988 scene render ordering repair (2026-09-25)

The final review snapshot identified concurrent explicit scene renders bypassing
the session operation queue. GLB and FBX renders now enter the same queue as
snapshots, measurement and buffer exports, preserving independent commits and
failures before a later recovery. A buffer export queued between two renders
observes the first render.

Four new regressions failed against the prior implementation: both formats
coalesced the first render into the second and hid an earlier render failure.
With the fix, all fifteen scene tests passed. macOS arm64/Node.js 24.17.0 package
build/typecheck/lint and all **125 tests** passed, including installed-archive
CLI/MCP coverage; standalone example type checks also passed. This JavaScript
queue repair changes no native engine, asset bytes or prior visual evidence.

### Combined sprite/SFX merge validation

PR #989 merges `main` at `bcc35d49`, preserving the SFX extension and dependency-security updates while retaining sprites. Both component subpaths, native dispatch paths, measurement coordinate spaces and export extensions are registered together. Installed CLI/MCP fixtures cover all six local formats, including `.wav` and `.sprite.zip`. The shared signal fixture retains the upstream 2.5-second delayed start for both Unix signals and the 30-second readiness deadline.

After resolving the merge, the macOS arm64 package build, typecheck, lint and standalone examples passed, as did all 115 package tests, all 77 repository CI contract tests, workflow validation and sprite/SFX/adapter Clippy with warnings denied. Historical evidence above remains tied to its recorded source state and format count.

On the merged code at `3e938a14`, public main/native candidate installation passed with six generated local formats. The final prepared root `TMPDIR=/private/tmp cargo test` run passed 1,938 tests with 3 existing opt-in tests ignored. Generated repository-owned `dist` output was removed after validation.


## PR #988 sprite integration merge repair (2026-09-25)

Merged `main` at `6debb999` (PR #989) without rebasing. Conflict resolution retains
both static GLB/FBX engines and the sprite engine in Cargo, the native adapter,
package subpaths, diagnostic vocabulary, CLI/MCP routing, installed consumers,
and six-host CI/release checks. Sprite output retains its `.sprite.zip` compound
extension; GLB/FBX retain their format signatures and world-bound MCP checks.
All existing feature contracts and historical acceptance records are preserved.

On macOS arm64 with Node.js 24.17.0, package build/typecheck/lint and all **132
tests** passed, including installed-archive CLI/MCP generation for all eight local
formats. Standalone example type checks, **81 CI contract tests**, and workflow
validation also passed. These checks do not claim new visual renders or remote
platform execution.


## PR #988 scene file-export ordering repair (2026-09-25)

File exports reserve both their session position and shared directory position
at invocation, preserving the preceding operation even if directory reservation
fails early. This prevents a later render from changing the exported tree or
revision and preserves file order across sessions while an earlier render is
still committing.

All **22 scene tests** passed on macOS arm64/Node.js 24.17.0. Four new GLB/FBX
regressions fail on the prior implementation with both free and occupied output
directories. Two cross-session regressions also reject the incomplete fix that
reserves a directory only after reaching the session queue. Two early-reservation
failure regressions prove that a rejected file export must retain the preceding
render's queue slot. Existing revision, disposal, cancellation and recovery tests
remain passing.


Final verification of implementation `cd7ac0a6` passed root
`TMPDIR=/private/tmp cargo test --locked -- --test-threads=1` with **1,944 passed,
zero failed and three existing opt-in tests ignored**, after the documented
frontend and separate generic/pnport preload preparation. Scene/exporter,
sprite/SFX and native-adapter Clippy passed with warnings denied. Package
build/typecheck/lint and all **139 tests** passed, as did standalone example
checks and public main/native candidate installation with eight CLI formats.
The earlier **81 CI contract tests** and workflow validation cover the unchanged
merged workflow configuration. Only macOS arm64 was run locally; the final push's
CI results and new visual renders are not claimed. Generated repository-owned
`dist` directories were removed after verification.


## PR #988 schema CI and event-matrix repair (2026-09-25)

Both failing jobs in run `36133345298` (DevHud Protocol and Client, and
async-commit-hook contracts/integration) failed when Buf cloned a pointer-only
local baseline and tried to smudge an unavailable, unrelated LFS font. The
breaking-check command now scopes the LFS smudge skip to Buf. A real Buf/Git LFS
fixture reproduces the old clone failure and verifies compatible schemas,
breaking-field rejection, and the existing absent-baseline behavior. A temporary
pointer-only clone of the full repository passed `proto:check`, Go binding tests,
and client lint/build with **37 tests**, without fetching LFS payloads.

The final status snapshot also discovered newly merged `main` commit `a00774d0`
(PR #986). The merge retains the event-specific CI matrix: ordinary affected
Windows/Linux validation, all six hosts in manual CI and release gates, and the
shared host script. Scene/GLB/FBX tests and Clippy run through that script; Linux
scene preparation and all four product render shards remain required. Both scene
jobs share the narrowed source paths, so documentation-only changes do not start
the native matrix or an orphaned render job. The protocol smudge fix independently
arrived on `main` too; the merge retains its behavior and the new regression.

Local macOS arm64/Node.js 24.17.0 merge validation passed **92 CI contract tests**,
workflow and shell syntax validation, **22 affected release-contract tests**,
React Forge build/typecheck/lint with **139 tests** and standalone example checks,
and the full DevHud app `pnpm test` plus frontend build. The imported schedule
coordinator lifecycle regression passed three consecutive runs. This evidence
does not claim new renders or hosted results after the repair push.


The final merged root `TMPDIR=/private/tmp cargo test --locked --
--test-threads=1` run passed **1,944 tests**, with zero failures and three existing
opt-in tests ignored. Clibox, the scene/exporter/sprite/SFX engines and the native
adapter passed Clippy with warnings denied; repository formatting also passed.
The root test used the documented frontend and separate generic/pnport preload
preparation. Generated repository-owned `dist` directories were removed, and
`git lfs fsck` passed before the single repair push.


## PR #988 public-guide merge repair (2026-09-25)

Merged `main` at `6b8b6cfe` (PR #991) without rebasing. The grouped navigation and
Sprite preview remain intact, and GLB/FBX join the format-directory layout. All
eighteen guides are retained, with permanent redirects for both spellings of
each of the nine former format routes. The package README and internal route
contracts use the new canonical GLB/FBX links. Rendered validation checks the
unreleased npm `0.1.1` notices for all four preview formats.

On macOS arm64/Node.js 24.17.0, `pnpm test` from `apps/public-docs` passed the
**25 site-selector tests**, full Rspress build, both rendered-document validators,
and **15 validator regressions**. This documentation-only merge changes no
native engine, model, texture, or previously recorded rendering evidence.
