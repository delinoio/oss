# React Forge (private workspace)

The private `@delino/react-forge` package authors PPTX, DOCX, XLSX, and independent tagged PDF through persistent React sessions. It imports existing Office packages, exposes supported editable regions, and preserves unrelated XML and package parts when mounting React content into those regions. Its executable is named `react-forge`.

Use Node.js 24 on macOS arm64 and the repository-pinned Rust toolchain. React 19.2.8 and react-reconciler 0.33.0 are pinned together. Build the native binding explicitly:

```sh
pnpm install
pnpm --filter @delino/react-forge build
pnpm --filter @delino/react-forge cli run examples/presentation.tsx --output /tmp/report.pptx
pnpm --filter @delino/react-forge cli run examples/document.tsx --output /tmp/report.docx
pnpm --filter @delino/react-forge cli run examples/workbook.tsx --output /tmp/report.xlsx
pnpm --filter @delino/react-forge cli run examples/pdf.tsx --output /tmp/report.pdf
pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge
```

The package remains private and workspace-only. Generation has no Office, LibreOffice, Python, external conversion or runtime download dependency. Generated `dist` is untracked and removed from final worktrees. The tests also pack the built package into a temporary consumer to exercise its installed CLI.

| Import | Authoring capabilities |
| --- | --- |
| `@delino/react-forge/pptx` | Rich text/lists, PNG/JPEG, shapes, merged tables, editable bar charts, connectors, row/column/canvas layout |
| `@delino/react-forge/docx` | Paragraphs/headings/lists, merged tables, images, hyperlinks, sections, page breaks, headers/footers, editable bar/line/pie charts |
| `@delino/react-forge/xlsx` | Typed cells/formulas, formatting, merges, dimensions, freeze panes, filters, links, editable bar/line/pie charts, five conditional-format and seven validation families |
| `@delino/react-forge/pdf` | Independent pages, flow text/lists/tables, automatic pagination, repeated headers, images/shapes/links and semantic tags |

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Presentation, Slide, Column, Text } from "@delino/react-forge/pptx";

export default async function task({ data, signal }) {
  signal.throwIfAborted();
  const session = createSession(Format.Pptx);
  await session.render(
    <Presentation>
      <Slide><Column padding={32}><Text>{data?.title ?? "Hello"}</Text></Column></Slide>
    </Presentation>,
  );
  return session;
}
```

Import React in TSX tasks when using the classic JSX transform. The bundled `tsx` loader respects caller TypeScript configuration and supports Node 24 ESM/CommonJS default-export namespaces. Run an installed package with `react-forge run <entry.tsx> --output <file>`. Task data is inline JSON (`--data '{"title":"Example"}'`); `--json` enables structured results, and `--overwrite` permits replacement. Help and version are available. Caller TSX executes with ordinary caller permissions; this is not a sandbox.

Library callers dispose sessions in `finally`. Common exports include `createSession`, `importOffice`, `DocumentSession`, `Format`, `ErrorCode`, `ForgeError`, `limits`, and `capabilities`. Session methods are `render`, `registerImage`, `registerFont`, `inspect`, `mount`, `measure`, `exportBuffer`, `exportFile`, `subscribe`, and `dispose`.

```tsx
import React from "react";
import { importOffice, Format } from "@delino/react-forge";
import { Paragraph } from "@delino/react-forge/docx";

const session = await importOffice(Format.Docx, { path: "/tmp/original.docx" });
try {
  const target = session.inspect().targets.find(t => t.kind === "paragraph" && t.editable);
  if (!target) throw new Error("No supported paragraph was found.");
  const region = await session.mount(target, <Paragraph>Updated content</Paragraph>);
  await region.render(<Paragraph>Revised content</Paragraph>);
  await session.exportFile("/tmp/edited.docx");
  await region.unmount(); // Restores this region from the original imported model.
} finally {
  await session.dispose();
}
```

The CLI equivalent is in `examples/edit-office.tsx`, taking `format`, an explicit local `source` path, and `text` in `--data`. Documents/assets accept `Uint8Array` or `{ path }`; URLs are never fetched. Imported target handles are session-scoped. Unsupported regions stay opaque and cannot be mounted. Overlapping mounts, ambiguous identities, invalid references, and edits whose preservation cannot be established fail explicitly. Macro-enabled, encrypted, signed, Strict and legacy Office packages are rejected. Newly authored structural changes use root React updates. PDF import/editing and Office-to-PDF conversion are excluded.

React components support state, reducers, Context, refs/imperative handles, effects/cleanup, Strict Mode, classes/error boundaries, memoization, profiling, external stores, lazy/Suspense/use, transitions/deferred values, action state, and Activity. Hidden Activity content is excluded from export. Export waits for relevant Suspense work and registered assets, then pins the model and fonts. Later changes cannot alter that export. Independent asynchronous effect work remains the caller's responsibility. An invalid latest render fails instead of exporting old content.

Refs hold `NodeHandle` values. After an export establishes a revision, use `measure(handle, { revision: session.revision })`; stale revisions fail. `useLayoutEffect` follows React commit timing, not native layout completion. PPTX/PDF geometry uses page coordinates. DOCX authoring geometry is `word_flow` in points before Word pagination; mounted Word content uses `mounted_region`. XLSX authoring uses `worksheet` coordinates and declared dimensions with conventional column-width conversion. PDF returns the first fragment of a split node. Nodes without geometry fail with `invalid_target`. Office applications own final font substitution, layout and pagination.

System font discovery/fallback is the default. Use `createSession(format, { systemFonts: false })` and `await session.registerFont(bytesOrPath)` for caller fonts only. New text is shaped and checked for CJK, RTL/mixed direction, missing glyphs and color emoji. Word runs materialize selected fallback families. Office output references font families; install those fonts in the consuming Office environment. PDF embeds subsets when licensing permits; restricted, no-subsetting and bitmap-only embedding fail. A missing-font diagnostic identifies the remedy: register/install suitable fonts, correct the input, and retry. Untouched opaque source content is not font-validated. Environment-dependent layout is expected; existing Forge keeps its bundled default font.

Spreadsheet formula caches are optional caller-supplied numbers, strings or Booleans. Missing caches remain absent and the workbook requests recalculation. There is no formula engine. Native charts retain editable associated workbook data. PDF retains headings, paragraphs, lists, table headers/cells, links, alternative text, language and logical reading order. Repeated table headers are visual artifacts with one logical header. Oversized indivisible content produces `layout_overflow`. No PDF/UA conformance is claimed.

Pass `AbortSignal` to import, asset, measurement or export options. Unresolved work has no automatic timeout; dispose cancels and joins owned work. SIGINT/SIGTERM cancel CLI work, clean up the session and exit 130/143. Failed tasks do not publish output. File export rejects existing destinations by default. Explicit overwrite uses same-filesystem temporary output, flushing, atomic publication and parent-directory synchronization. Replacing an imported source verifies its original fingerprint; concurrent or external changes produce a conflict. Cancellation before publication preserves the existing file. A durability failure after publication reports `published: true`. Disposal never deletes exported files. Correct input and retry; rollback uses a repository revision and rebuild without rewriting old exports.

`ForgeError.code` distinguishes malformed input, unsupported packages/edits, invalid targets, conflicts, resource limits, missing fonts, overflow, cancellation, disposed sessions, I/O and React-render failures. Safe context includes applicable stage, format, revision and bounded model location. Diagnostic subscriptions include operation/stage, revision, duration and classification. Native tracing is operation-scoped without a global subscriber; callback exceptions cannot change outcomes. Events exclude document text, XML, bytes, credentials and host paths.

| Resource | Ceiling |
| --- | ---: |
| Office input/output and PDF output | 256 MiB |
| Expanded Office package | 512 MiB |
| ZIP entries / individual part | 10,000 / 64 MiB |
| XML depth / nodes per part | 128 / 1,000,000 |
| Newly rendered tree bytes / depth / nodes | 16 MiB / 48 / 20,000 |
| Individual image | 64 MiB and 64,000,000 pixels |

`limits` and `capabilities` expose these budgets. All registered assets together are bounded to 256 MiB, with 64 MiB per explicit font. Applicable existing PPTX constraints remain, including 1,000 slides and tables of at most 1,000 rows × 128 columns. Chart data expansion is bounded to 200,000 cells and XLSX merge expansion to 250,000 cells. Opaque imported content uses package budgets. Integrators own authentication, isolation and process-wide resource governance.

Test-only rendering uses LibreOffice, Poppler and the pinned dependencies in `scripts/render-requirements.txt`. After building, run `pnpm --filter @delino/react-forge test:render --output /tmp/react-forge-render`. Optional `REACT_FORGE_SOFFICE`, `REACT_FORGE_PDFTOPPM` and `REACT_FORGE_PYTHON` select explicit test tools. Run `pnpm --filter @delino/react-forge benchmark --output /tmp/react-forge-benchmark.json` for representative, external-edit and near-limit samples. Reports record tool/font provenance and time, peak RSS and event-loop delay; there is no performance SLO. This evidence is not direct Microsoft Office validation.

Canonical internal ownership, acceptance evidence and complete requirements live in `docs/project-react-forge.md` and its linked contracts. Public distribution, hosting, watch mode, a new MCP interface and a GUI are outside this project.
