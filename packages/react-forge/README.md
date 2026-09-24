# React Forge (private workspace)

The private `@delino/react-forge` package authors PPTX, DOCX, XLSX, independent tagged PDF and editable Figma Design files through persistent React sessions. It imports existing Office packages, exposes supported editable regions, and preserves unrelated XML and package parts when mounting React content into those regions. Its executable is named `react-forge`.

Use Node.js 24 on macOS, Windows or glibc Linux (x64 or arm64) and the repository-pinned Rust toolchain. React 19.2.8 and react-reconciler 0.33.0 are pinned together. Build the native binding explicitly:

```sh
pnpm install
pnpm --filter @delino/react-forge build
pnpm --filter @delino/react-forge cli run examples/presentation.tsx --output report.pptx
pnpm --filter @delino/react-forge cli run examples/document.tsx --output report.docx
pnpm --filter @delino/react-forge cli run examples/workbook.tsx --output report.xlsx
pnpm --filter @delino/react-forge cli run examples/pdf.tsx --output report.pdf
pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge
```

The supported native hosts are macOS x64/arm64, Windows x64/arm64 (MSVC), and glibc Linux x64/arm64. Install the matching Rust target and platform build tools (Xcode command-line tools, MSVC C++ Build Tools, or a Linux C toolchain). Linux builds also need `pkg-config` and the Fontconfig development package (`libfontconfig1-dev` on Ubuntu); runtime font discovery needs Fontconfig. Install appropriate CJK/RTL/color-emoji fonts, or register caller fonts. Alpine/musl and other architectures are unsupported. Each build creates an artifact for the current host; rebuild when moving a workspace or private archive to another host. `capabilities.runtime.hosts` enumerates the supported IDs.

The package remains private and workspace-only. Local-document generation has no Office, LibreOffice, Python, external conversion or runtime download dependency. Generated `dist` is untracked and removed from final worktrees. The tests also pack the built package into a temporary consumer to exercise its installed CLI.

| Import | Authoring capabilities |
| --- | --- |
| `@delino/react-forge/pptx` | Rich text/lists, PNG/JPEG, shapes, merged tables, editable bar charts, connectors, row/column/canvas layout |
| `@delino/react-forge/docx` | Paragraphs/headings/lists, merged tables, images, hyperlinks, sections, page breaks, headers/footers, editable bar/line/pie charts |
| `@delino/react-forge/xlsx` | Typed cells/formulas, formatting, merges, dimensions, freeze panes, filters, links, editable bar/line/pie charts, five conditional-format and seven validation families |
| `@delino/react-forge/figma` | Remote editable pages, Auto Layout, text, vectors, images, components/variants/instances, variables and styles |
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

Pass `AbortSignal` to import, asset, measurement or export options. Unresolved work has no automatic timeout; dispose cancels and joins owned work. On Unix, SIGINT/SIGTERM cancel CLI work, clean up the session and exit 130/143. On Windows, console Ctrl+C/Ctrl+Break clean up and exit 130; programmatic termination (including Node child.kill) is forceful and cannot promise cleanup. Failed local-document tasks do not publish output. Figma remote outcomes are described below. File export rejects existing destinations by default. File exports in the same directory run in call order across sessions, including directory aliases; a queued export can be cancelled. Buffer exports and exports to different directories remain independent. Explicit overwrite uses same-filesystem temporary output, flushing, atomic publication and Unix parent-directory synchronization. Windows flushes file bytes before publication; Node does not expose directory fsync there, so crash durability of the directory entry is not guaranteed. Imported source files and their canonical path aliases cannot be overwritten, even with `overwrite: true`; export to a separate path. A fingerprint check followed by a rename cannot protect another application's concurrent save. Cancellation before publication preserves the existing file. A durability failure after publication reports `published: true`. Disposal never deletes exported files. Correct input and retry; rollback uses a repository revision and rebuild without rewriting old exports.

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

## Figma Design

Figma creation and editing use the official remote Figma MCP server. `render` updates the local React tree; `publish()` applies it to Figma. The same CLI writes a `.figma.json` receipt containing the file URL, revision, node bindings, image hashes and outcome. It is not a `.fig` file and contains no authentication token.

```tsx
import { createSession, Format, CredentialSource, openFigma } from "@delino/react-forge";
import { Document, Page, Frame, Text } from "@delino/react-forge/figma";

const session = createSession(Format.Figma, {
  fileName: "Travel companion",
  planKey: selectedPlanKey, // Select a plan reported by official Figma MCP whoami.
  credentials: { source: CredentialSource.Codex },
});
try {
  await session.render(<Document><Page name="Mobile">
    <Frame name="Home" width={390} height={844} fill="#F5F1E8">
      <Text name="Title" fontSize={32}>Find your somewhere.</Text>
    </Frame>
  </Page></Document>);
  const result = await session.publish();
  console.log(result.url, result.status);
} finally {
  await session.dispose();
}

const editing = await openFigma(fileUrlOrReceipt);
try {
  const page = editing.inspect().targets.find(t => t.kind === "PAGE");
  if (!page) throw new Error("Select a page first.");
  await editing.refresh({ pageId: page.remoteId });
  const title = editing.inspect().targets.find(t => t.name === "Title" && t.kind === "TEXT");
  if (!title) throw new Error("Select an editable title first.");
  await editing.mount(title, <Text>Make room for the unexpected.</Text>);
  await editing.exportFile("/tmp/edited.figma.json");
} finally {
  await editing.dispose();
}
```

`openFigma` accepts a Design URL, file key or previous receipt. `refresh({ pageId, nodeIds })` narrows reads to up to 24 explicitly selected IDs and their ancestors; omit `nodeIds` to inspect the page. `refresh({ resources: true })` inspects local variables and paint/text styles. Use `target={remoteId}` to explicitly select existing children within a mounted frame. Their kind and parent stay fixed. Properties you omit and children you do not select remain under the original author's control. Removal of an owned container containing unselected children is rejected. Unmounting relinquishes the React region while preserving its last published state.

Components include pages, frames/Auto Layout, text, rectangles/ellipses/lines/vectors, registered PNG/JPEG images, components, component sets, instances, variable collections/variables, paint styles and text styles. `nodeKey` declares a local reference for instances, styles and variable bindings. Colors use `#RRGGBB`; `fontName` uses a font available in the Figma file, with its exact style spelling. Figma handles font availability and rendering; local font registration and binary export belong to the Office/PDF sessions. Images are limited to 10 MiB and 64 million pixels each.

Connect Figma in Codex or Claude Code first. Choose `CredentialSource.Codex`, `ClaudeCode`, or `Auto`. Automatic selection stays pinned for that session. Expired or rejected credentials are reread once; further authentication requires reconnecting Figma in the selected application. React Forge never refreshes or changes that application's credentials.

Remote publication can be **complete**, **partial**, or **unknown**. A `FigmaPublishError` carries the receipt. Confirmed IDs survive partial failures; retrying a known partial batch does not recreate them. Unknown outcomes require inspecting/reopening the file, and ambiguous new-file/node creation is never automatically repeated. Cancellation stops later batches; it does not undo existing changes. File-output conflicts are checked before publishing, but a receipt-save failure can occur after Figma has changed, so inspect the attached receipt. An unchanged session publish makes no remote calls. Request admission and file queues are shared by sessions in the same process; other clients remain subject to server limits and optimistic conflict guards.

The examples `travel-figma.tsx` and `travel-figma-edit.tsx` create the five-screen fictional ROAM app and reopen it to change text, an image, layout and itinerary content:

```sh
pnpm --filter @delino/react-forge cli run examples/travel-figma.tsx --data '{"planKey":"team::YOUR_SELECTED_TEAM"}' --output /tmp/roam.figma.json
pnpm --filter @delino/react-forge cli run examples/travel-figma-edit.tsx --data '{"fileKey":"YOUR_FILE_KEY"}' --output /tmp/roam-edited.figma.json
```

The generation task creates a new screen page; use the edit task for subsequent runs. The edit task explicitly reuses the existing note when run again. Image provenance is shared with the travel investor example.
