# React Forge Native Contract

## Scope
Shared `forge-package` and `forge-document`, format-specific `forge-docx`, `forge-xlsx`, `forge-pdf`, and dedicated `react-forge-node` adapter. `forge-document` owns reusable text/style, image and native chart/data primitives without merging format-specific document models. Reuse `forge-tree-doc` and `forge-pptx` for presentations. Complete scope is preserved in [requirements](packages-react-forge-requirements.md).

`forge-figma` adds an independent pure revision planner. It validates Figma entities and references, differences declared ownership and partitions dependency-ordered operations by page, UTF-16 code size and a bounded result count. The Node layer owns authentication, SDK transport, rate admission, guarded canvas execution and recovery. N-API also validates Figma image bytes with the existing bounded native image decoder. See the [remote contract](packages-react-forge-figma-contract.md).

## Runtime and Language
Rust on the repository-pinned toolchain. Engines do not depend on N-API, Office, LibreOffice, Python, external converters or runtime installation. Rust is selected over repository-default Go to reuse the Forge engines and native document libraries.

## Users and Operators
The Node session library and repository engine developers; existing Forge CLI/MCP consumers retain their interfaces and font defaults.

## Interfaces and Contracts
Four separate document models share bounded package, style, font and asset infrastructure where applicable. Office charts remain editable, including their associated data. Spreadsheet formulas retain optional caller-provided cached values and request application recalculation; no formula calculation engine exists. PDF authoring is independent of Office and includes flow pagination, repeated table headers, actionable indivisible-content overflow and semantic reading-order tags; PDF import/editing and PDF/UA claims are excluded.

Imports retain unsupported content as opaque bytes. Edit only provably owned supported regions; preserve all unrelated package payloads and outside-region XML bytes. Reject encrypted/signed/macro/legacy/Strict packages, ambiguous identities, dangling references and unsafe edits. Positive externally authored fixtures must demonstrate actual supported edits, including charts and spreadsheet rules.

Limits: Office input/output and PDF output 256 MiB; expanded Office 512 MiB; 10,000 ZIP entries; individual ZIP/XML part 64 MiB; XML depth 128 and 1,000,000 nodes per part; newly rendered tree 16 MiB, depth 48 and 20,000 nodes; image 64 MiB and 64,000,000 pixels. Preserve stricter existing PPTX limits. Opaque content uses package limits, not newly rendered tree limits.

React Forge defaults to system font discovery/fallback with explicit caller fonts. Check new content for missing glyphs, CJK, RTL/mixed direction and color emoji; report typed actionable failures instead of missing/replaced glyphs. Honor embedding permissions and never require untouched opaque content to be rendered. Do not change existing Forge's bundled default font policy.

### Rust component integration

- Follow `crates-react-forge-contract.md` and the complete issue #968 requirements. `forge-package` owns shared bounded OOXML preservation; `forge-document` owns shared text/style, asset and chart primitives; `forge-docx`, `forge-xlsx`, and `forge-pdf` own independent models and engines. `react-forge-node` is only the private N-API adapter.

- System discovery and caller fonts apply to React Forge only. Presentation inspection must not require font setup before the caller can register fonts. Check newly rendered content without rendering untouched opaque source. Keep all packages unpublished.

- Emoji fallback must prefer supported color families on complete emoji graphemes before general text families, including with caller fonts only. Preserve ordinary digits/spacing and explicit text presentation selectors; do not globally prioritize emoji fonts for all text.

- Keep React Forge font shaping and PDF tagging operation-owned. `TextLayout` injection and explicit `FontEmbedding` selection must preserve existing Forge API defaults. Office font references do not imply embedded caller fonts. PDF subset embedding must enforce licensing flags, and repeated visual table headers must remain pagination artifacts outside the logical reading order.

- React Forge preserving PPTX updates must restore source-digest-bound node and image identities across independent native imports. External packages without Forge metadata must support no-op byte preservation and repeated mounted edits.

- React Forge resource tests must cover accepted boundaries as well as over-limit rejection. Preserve iterative XML preflight and bounded-stack handling for valid deep XML until the upstream recursive parser has a proven safe stack bound.

- Word drawing replacement owns only supported inline content; foreign paragraph/run attributes and surrounding bookmark/field/revision markers must stay opaque. Validate extension namespaces before exposing a chart as editable.

- Spreadsheet rule editability requires modeled attributes on every rule and nested threshold/color element; unsupported precedence or rendering properties remain opaque even without extension namespaces.

- DOCX text backgrounds use native run shading; paragraph defaults must be materialized on text runs with explicit run overrides retained.

- React Forge must retain six native macOS/Windows/glibc Linux x64/arm64 targets. Enable all system-font tests in its prepared host matrix and validate Windows cancellation in an isolated real console, never by treating Node process.kill as a console event.

- Presentation text editability must validate paragraph/run semantics as well as body geometry, including table-cell text. Preserve unmodeled fields, links, bullets, defaults and extensions as opaque content.

- Imported PPTX update envelopes must contain replacements and source identity, not a serialized copy of untouched Office content. Apply the React-tree input ceiling to authored work without making successfully imported large documents unexportable.

- Imported DOCX mounted measurements must use source section/cell flow constraints. Preserve unknown width as unavailable geometry while retaining safe edits; never substitute a default page width for ambiguous source layout.

- pnport macOS multi-group ownership must use native process-birth admission and kernel audit-token signals. Treat inventory and preload registration files only as discovery requests; authenticate accepted recovery records and registration outcomes with a per-run Ed25519 signing key shared solely by the supervisor and same-image guardian. Inject only the pinned public verification key, strip caller-supplied copies before restoring it through exec environment replacement, and require matching verified acknowledgements before group/session changes. Before publishing a signed admission, publish its complete native identity in a fixed-size shared mapping backed by an unlinked private file, inherited solely by the authenticated same-image guardian. The supervisor mapping is read-only; retain decoded versions in supervisor-owned memory and drain all bounded completed slots before guardian-loss recovery. Deletion of writable journal files must never erase historical ancestry. A release/acquire publication count exposes only complete immutable slots; a partial final slot never grants ownership. The retained mapping survives guardian exit and accommodates every bounded version without socket backpressure or a stopped supervisor acknowledgement. Bound slots to 1,024-byte payloads and history to 65,536 versions; failed replication closes new admission while proven cleanup authority remains available. Never expose the descriptor to user children or obtain authority from an incomplete copy. Invalid journal content still fails foreground recovery before tty transfer; private admitted history remains available for shutdown. Changed/exited images receive signed terminal responses rather than leaving native callers waiting. Retain the initially captured setpgid target birth through admission so exit between the preliminary check and registration also preserves native ESRCH instead of recording an injection failure. Prove descendant-selected tty foreground through admitted native births before restoring it to the caller, including authenticated parent recovery after guardian loss before or during the foreground query. After a job actually claimed the tty, restoration may also reclaim its currently verified empty foreground group using a fresh native signal-zero existence probe and unchanged-foreground comparison; this grants no signalling authority and never displaces a live unrelated group or gives an unclaimed background job foreground ownership. Darwin retains the empty foreground group object and reserves its PGID until the tty releases its reference. Preserve historical admitted image versions for reparented children, termination of all observed owned births before any SIGCONT, younger-descendant-before-parent ordering, the five-second grace and the original group reservation until final cleanup. Keep stable readiness closed until native detached, orphan, stop and owner-crash acceptance passes.

- pnport macOS abrupt-owner-loss tests must distinguish native orphan-group SIGHUP/SIGCONT from pnport termination requests with an unvirtualized native control. Preserve exact cancellation-signal assertions, termination-before-resume ordering, bounded complete cleanup, independent-group survival and the five-second unresponsive grace; forced OS termination cannot promise which native handler runs first.

- pnport native shutdown must not abandon an already proven birth because accepted-journal publication fails. Retain native shutdown authority in memory, keep new image admission closed, attempt bounded cleanup and report the infrastructure failure. Ownership directories follow private cache-directory validation. Retain only the closed failure stage and optional numeric native error in owner records and debug logs; never native error text or identities.

- pnport constructor-lease and pending-abandonment unit controls must isolate exact open-description lifetime assertions from concurrent test forks, while leaving the parent suite parallel. Retain an explicit inherited pre-exec descriptor control so an initializing lease is never mislabeled abandoned.

- pnport macOS unit fixtures resolve their prebuilt injection companion from the same Cargo profile for the legacy `deps` and pinned nightly `build/pnport/<unit-hash>/out` layouts. Keep this lookup test-only and retain the production adjacent-companion, native architecture and ABI-marker checks.

- Test pnport parent-error precedence over later namespace conflicts directly against the shared resolver. Native prefix-error controls use a valid namespace; runtime conflict children separately verify fatal detection because supervisor shutdown can precede their assertions.

- pnport root missing-entry rejection rechecks current entry/readiness and native failure records at the rejection boundary. Log only closed deadline actions and state flags; passing later runs does not establish the cause of intermittent startup failures. Native signal-marker fixtures must block concurrent handled termination signals until the first handler completes its marker and exit.

- pnport new-group terminal fixtures must verify native foreground placement before issuing explicit Ctrl+Z. Do not rely on an incidental background-read SIGTTIN that can disappear when supervisor placement wins scheduling; retain the independent background-read control.

- clibox min-repro selects staged root executables through the same verified observed alias/identity mapping as required inputs. Preserve original Unix argv zero, literal arguments, external executable handling and required source/link verification.

- clibox port termination consumes every original private socket observation before public endpoint deduplication. Retain one signal per PID, birth checks, original socket matching and the shared verification deadline; replacement sockets grant no authority.

- clibox assetcov human reports share the complete covered/uncovered list formatter on all platforms. Preserve native path display, JSON, quiet/explicit-file publication, thresholds and child status.

- clibox min-repro retains cancellation through final report encoding, commit and success return. Preserve committed bundles/reports; remove only unpublished report staging and retain prior destinations on pre-commit cancellation.

- clibox Windows run env and wrappers expand supported executable dollar references against the prepared child environment before native PATH/PATHEXT lookup. Preserve literal argument conversion, parent-only assignment values and deferred workload resolution after wrapper admission.

## Storage
Memory-only native operations and explicit local files; no database/cache/recovery archive. Local storage is an explicit R2 exception. File publication belongs to the session's revision/cancellation boundary, with same-filesystem temporary output and truthful publication results.

## Security
Bound before expensive parsing/decoding; reject unsafe ZIP paths, duplicate identities, DTDs/entities, malformed structures and invalid references. Never execute embedded content or follow remote relationships. JavaScript values, functions, hooks and callbacks never enter worker computations. Native cancellation flags are scoped to each synchronous operation and restored on return or unwind; independent workers never share implicit cancellation state. Checkpoints interrupt ZIP inflation/compression in 64 KiB chunks, XML preflight and traversal, font/run/glyph work, layout nodes/pages/rows, and Office generation/import/edit loops. Third-party parser, shaping and final serialization calls remain indivisible bounded units; cancellation is checked around them rather than claiming preemption inside those libraries.

## Logging
Operation-scoped tracing without global logger installation. Emit safe operation/stage/format/revision/duration/error fields, never parser messages containing contents, host paths or bytes.

## Build and Test
Run root `cargo test` after required generated app prerequisites, plus targeted native and Node integration tests. Cover package resource/security limits, unchanged bytes, native chart/workbook edits, spreadsheet rules, fonts and PDF semantics. Test-only LibreOffice/Poppler may render outputs; record versions and font provenance. Such evidence is not Microsoft Office validation.

DOCX paragraph and run backgrounds emit RGB run shading (`w:shd`), with paragraph defaults inherited into runs and explicit run colors taking precedence ([Open XML shading](https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.wordprocessing.shading?view=openxml-3.0.1)). The DOCX engine has structural tests for rich text, headings, lists, sections, headers/footers, page breaks, merged cells, images and native bar/line/pie charts with editable embedded workbooks. Its python-docx 1.2.0 fixture verifies supported paragraph/cell edits and exact preservation of unselected parts and XML, plus opaque-equation refusal. The complementary renderer evidence and reproduction commands are recorded in [validation](packages-react-forge-validation.md).

## Dependencies and Integrations
Forge foundation from PR #967 is a repository dependency. Shared extraction must retain its regression coverage. All new crates remain unpublished. Native output is generated, untracked and rebuilt explicitly.

## Change Triggers
Update the Node contract, project index, Forge foundation when shared behavior changes, relevant ownership rules and evidence in the same change.

## References
- [Project](project-react-forge.md).
- [Requirements](packages-react-forge-requirements.md).
- [Forge foundation](crates-forge-foundation.md).
- [Repository defaults](repository-defaults.md).

## Spreadsheet Engine
`forge-xlsx` owns independent workbook models and native worksheet/chart emission. Formula caches are caller-provided and typed; omitted caches remain absent, and workbook calculation properties request application recalculation. Five conditional-format families and seven validation families have independent generation and external-fixture edit tests. Supported cell edits preserve original styles unless an explicit replacement format is supplied; replacement formats append resources without rewriting existing style children. Chart replacement adds isolated worksheet data and retains unrelated source data and chart parts. Spreadsheet charts containing foreign element or attribute namespaces remain opaque, including markup-compatibility alternatives. Imported cells and rules retain their original address/range. Validation edits preserve imported input/error message visibility, including absent false defaults. Unmodeled validation titles, non-stop error styles, hidden dropdowns, unknown attributes and formula attributes remain opaque. Conditional rules also whitelist attributes on every nested element; stop-if-true precedence, threshold inclusivity, bar lengths, icon percentage mode, tinted/theme colors and other unmodeled properties remain opaque. Unknown extensions remain opaque.

The external workbook fixture is generated with openpyxl 3.1.5; its generator and provenance are committed beside the fixture. These structural and preservation checks are not renderer or Microsoft Office validation.

## PDF and Font Engine
`forge-document::fonts` uses pinned Parley 0.9.0/Fontique 0.9.0 for operation-local system discovery, caller-provided fonts, shaping and fallback. It checks shaped glyphs and color-emoji representations. PDF subset embedding rejects restricted, no-subsetting and bitmap-only embedding permissions instead of bypassing them. Office output references font families; it does not embed caller/system font files. Office applications own final fallback, layout and pagination.

Color-capable caller families and the platform emoji generic take precedence over general text families only on complete emoji graphemes. This avoids Fontconfig selecting a monochrome DejaVu glyph before Noto Color Emoji. Classification uses the same pinned Unicode properties as Parley; segmentation preserves joined sequences, ordinary digits/spacing and explicit text presentation selectors. Caller-only emoji generics contain color-capable registered families, while normal text retains caller ordering. Selected glyphs still undergo color and embedding validation.

`forge-pdf` uses pinned Krilla 0.7.0 to generate independent tagged PDFs. Flow paragraphs split at shaped line boundaries. Table rows split across pages, initial header rows repeat as visual pagination artifacts, and the semantic tree retains one logical table header. Tagged headings, paragraphs, lists, cells, links and figures preserve logical reading order, alternate text and document/run language. Each authored node has revision-specific page fragments; the first fragment is the initial geometry. Oversized indivisible content fails before output publication. No PDF/UA conformance is claimed.

The presentation engine exposes operation-owned text measurement and an explicit `FontEmbedding` selection. Existing `generate`/`update`/layout APIs still select the previous pinned default and OFL embedding. React Forge selects system/caller shaping and reference-only font output. This extension must retain the complete existing Forge regression suite.

Current validation includes structural PDF pagination/tagging, system CJK/RTL/color-emoji output, explicit-font and missing-font behavior, and React PDF export/measurement. Poppler page rendering and pypdf extraction have also been inspected locally. Reproducible renderer, benchmark, CLI and CI evidence is recorded in the validation contract.

Office font validation materializes the selected fallback family into newly authored Word runs while preserving logical text, run styles and hyperlinks. Macintosh-only font name records are decoded as well as Unicode records. Standalone Word chart series use explicit RGB colors so imported documents do not require a theme rewrite for visible data.


Preservation hardening allocates Word drawing/list IDs against existing packages, appends numbering definitions without renumbering existing lists, and retains selected cell wrappers/namespaces. Explicitly empty authored section headers/footers suppress Word inheritance. External python-docx/openpyxl chart fixtures exercise all three Word chart replacement families. Foreign spreadsheet rule attributes remain opaque. New chart data and worksheet merges are bounded before XML expansion (200,000 chart data cells; 250,000 expanded worksheet merge cells). Duplicate dimensions fail, hidden chart-data sheets remain plotted, and appended differential number formats receive unused custom IDs. Boolean text styles retain explicit false overrides.

ZIP declared expansion is checked before inflation, with independent actual-byte checks. Tests cover expanded-size, entry, XML node/depth, image pixel and chart expansion boundaries. Font fixtures also cover restricted, no-subsetting and bitmap-only embedding flags. PDF semantic-order tests resolve page/MCID pairs through the structure tree; the first table header remains with the first body line.

Presentation inspection parses structure and captures source identity without discovering fonts or computing layout, so caller-only sessions can register fonts after import. Native geometry and new-content font validation occur during export or measurement. Imported presentation sessions bind the original package digest to a UUID-v7 traversal snapshot, including image asset keys and connector targets. Re-importing bytes for an update restores those identities before comparison, so externally authored packages need no pre-existing Forge metadata and no-op exports retain their original bytes. JSON geometry parsing retains exact floating-point round trips.

XML preflight enforces the published depth before parsing. Valid XML with at least 32 nested elements uses a scoped 16 MiB parser stack because the recursive roxmltree tokenizer has large unoptimized frames; shallow parts remain on the caller stack. Remove this compatibility boundary only after iterative parsing or a proven supported-depth stack bound. Output-byte checks are exercised at and above 256 MiB independently of expensive serialization; package input, part/aggregate expansion, XML depth/nodes, image bytes/pixels and React tree byte/depth/node boundaries have explicit tests.

Word regions with foreign paragraph/run attributes or bookmark/field/revision markers around drawings remain opaque. Drawing and chart replacements reject foreign extension namespaces instead of dropping them; selected cell wrapper attributes remain byte-preserved. Ordinary external paragraphs, cells, images and native chart families retain positive edit coverage.

The 0.9.0 font stack includes upstream CoreText enumeration and macOS CJK fallback repairs ([enumeration](https://github.com/linebender/parley/pull/536), [CJK fallback](https://github.com/linebender/parley/pull/598)). This avoids missing system fonts outside legacy Library/Fonts scans and unsupported PingFangUI outlines on macOS 15. Swash 0.2.10 remains an explicit name-table decoder for legacy Macintosh font names; it no longer relies on a Parley re-export.

React Forge native targets cover macOS/Windows/glibc Linux on x64 and arm64. Parley/Fontique select CoreText, DirectWrite or Fontconfig system discovery. Caller fonts and typed missing-glyph/color/embedding errors apply identically on every target. Dedicated native-host CI enables the non-macOS system-font tests after installing fonts; ordinary Cargo runs may omit those environment-dependent fixtures. Windows console integration requires the built Node workspace and explicitly enabled isolated-console test.

Word paragraphs containing page/column breaks or non-default text-wrapping clearance remain opaque, including inside selected tables/cells. Only ordinary line breaks (implicit or explicit `textWrapping`, with absent/`none` clearance) are admitted to editable paragraphs; unrelated edits preserve the protected break XML.

Every package relationship part requires an OPC-namespace `Relationships` root and leaf `Relationship` children. Matching attribute names on foreign or incorrectly named elements do not grant relationship authority; malformed relationship elements reject the package before reference resolution.

The XML depth ceiling counts nested elements, with the document element at depth one. Exactly 128 levels are accepted for both paired and self-closing elements, including text/comment children at the deepest level; 129 levels are rejected by iterative preflight before recursive parsing.

Shared text alignment retains omission separately from explicit left alignment. DOCX/PDF resolve an omitted alignment to their inherited/default left behavior; XLSX leaves horizontal alignment absent to preserve Excel's General behavior for each value type. Applying number formats, borders or wrapping alone does not force left alignment, including differential styles.

Spreadsheet differential styles preserve the three states of boolean text formatting: omitted inherits, true enables, and false emits an explicit OOXML reset (`b/i val="0"`, `u val="none"`). Both generated and mounted conditional-format rules can disable formatting inherited from the cell.

DOCX emission cascades cell text styles into nested paragraphs, runs and tables even for direct Rust engine callers. Paragraph and run properties override inherited values, including explicit false booleans; emitted run formatting matches the style used by font preparation and measurement.

DOCX authored list paragraphs may carry a shared UUID-v7 `instance_id`. Each list instance receives its own concrete numbering definition starting at one; items within that instance share it. React `List` supplies its stable reconciler identity, so adjacent or separated lists restart independently. Native callers omitting the identity retain the legacy per-kind sequence. New instances allocate unused package IDs without changing imported numbering definitions.

Imported PPTX updates send a bounded envelope of authored replacements, new asset references and the source-bound identity snapshot. The native worker reconstructs untouched content from the separately bounded Office source rather than sending the full imported text through the 16 MiB React-tree limit. Large supported imports remain exportable without edits and with small mounted replacements.

Word chart editability validates a closed ChartML/DrawingML structure and baseline property set, not just namespaces and plot type. Trendlines, error bars, logarithmic/custom axis bounds, stacked plots and other unmodeled standard features remain opaque. Supported native/generated and external bar/line/pie charts retain positive edit coverage; unrelated paragraph edits preserve protected chart parts exactly.

PDF semantic headings and table headers default to bold only when bold is omitted. Explicit false remains authoritative before shaping, and run-level overrides continue to win over the heading default.

Imported DOCX targets retain a source flow width in points. Body mounts use their enclosing section's page width minus horizontal margins; cell mounts and their nested paragraphs use the source preferred/grid width minus cell, row, table and inherited table-style margins. Omitted start/end margins follow the [Open XML 115-twip default](https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.wordprocessing.endmargin?view=openxml-3.0.1). Mounted geometry remains local authoring flow, not Word pagination or autofit. Unresolved widths (including multiple columns, gutter placement, conditional cell margins or headers shared by unequal sections) omit geometry, so measurement returns `InvalidTarget` while otherwise safe export remains available; they never receive a fabricated 468-point width.

## Static 3D extension

The generation-only GLB/FBX extension follows [the scene contract](packages-react-forge-scene-contract.md). `SceneSession` shares local publication and MCP lifecycle, uses independent world-space bounds and native scene engines, and introduces no runtime conversion dependency.

## Sprite Engine Extension
`forge-sprite` adds an independent bounded pixel model, deterministic CPU RGBA rasterization, PNG encoding and ZIP assembly; `react-forge-node/src/sprite.rs` connects the shared generation worker and diagnostics. It reuses cancellation and bounded ZIP output while preserving Office models and all existing font behavior. It does not use the OOXML-specific package reader. Follow the [sprite contract](packages-react-forge-sprite-contract.md) for exact dimensions, work/memory bounds, color/sampling semantics and archive metadata. Raster output is authored only; sprite import is rejected.

## SFX extension

`forge-sfx` independently validates and synthesizes the bounded sound model under the [SFX contract](packages-react-forge-sfx-contract.md). The N-API adapter dispatches format `wav` generation on an existing worker, emits operation-scoped diagnostics and returns PCM16 bytes plus timeline frames without font discovery. No React, audio device, filesystem or network I/O enters synthesis.

## 3D animation follow-up (Unreleased)

`forge-scene` owns joints, per-mesh inverse bind palettes, morph targets, clips
and the shared STEP/LINEAR/CUBIC evaluator. Morphs precede linear blend skinning;
measurement and FBX baking share validated sampler evaluation. The worker accepts
bounded copied FSG2 geometry and FSA1 sampler assets, retaining FSG1 compatibility.
Bind-palette expansion is checked against 256 MiB before each allocation. GLB
writes standard skin/animation/morph structures. FBX writes stacks/layers/curves,
LimbNode/Cluster bind data and BlendShape channels; importer-facing object names
use FBX's AnimStack/AnimLayer/AnimCurve class names. Rotation/CUBIC export uses
bounded frequency-controlled samples plus original keys, with Euler continuity
and the existing camera/light basis conversion. There is no JavaScript callback,
networking or external converter in native workers. Follow the complete
[scene contract](packages-react-forge-scene-contract.md).
