# React Forge Validation

## Scope
Acceptance evidence and repeatable validation for `packages/react-forge`, its native engines and the shared Forge boundary. The complete requirements remain normative; this document records evidence without expanding platform, performance, Office or accessibility claims.

## Runtime and Language
Node.js 24.20.0, React 19.2.8, react-reconciler 0.33.0, TypeScript 5.9.3 and pinned Rust nightly-2026-01-01 on macOS arm64. Test scripts use JavaScript and Python; production generation uses only JavaScript and Rust.

## Users and Operators
Repository developers and CI maintainers reproducing document, preservation and session behavior.

## Interfaces and Contracts
- `pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge` builds and validates the real library plus workspace and packed-consumer CLI.
- `pnpm --filter @delino/react-forge test:render --output <directory>` creates four formats, edits three external Office fixtures, renders their originals for comparison, and independently verifies package XML, extracted text, native chart visibility, CJK and PDF semantics.
- `pnpm --filter @delino/react-forge benchmark --output <report.json>` records eleven fresh-process workload samples.
- CI `react-forge` runs on the supported macOS arm64 runner. Its native/system-font work is uncached and its seven-day artifacts contain Office/PDF files, page PNGs, provenance and benchmark reports.

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
