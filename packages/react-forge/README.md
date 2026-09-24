# React Forge (private workspace)

React Forge implements the complete scope in [issue #968](https://github.com/delinoio/oss/issues/968). Development is ongoing: the current vertical slice connects persistent React sessions and the CLI to the existing Forge PPTX engine. DOCX, XLSX, independent tagged PDF, the new font policy and the full acceptance evidence are still in progress. Do not treat this intermediate checkpoint as completion of the issue.

Use Node.js 24 on macOS arm64 and the repository-pinned Rust toolchain. Install workspace dependencies at the repository root, then build explicitly:

```sh
pnpm install
pnpm --filter react-forge build
pnpm --filter react-forge exec react-forge run examples/presentation.tsx --output /tmp/example.pptx
pnpm --filter react-forge typecheck
pnpm --filter react-forge test
```

The package and native binding are private workspace dependencies. There are no runtime downloads or Office/Python/conversion dependencies. `dist` is generated and must not be committed.

```tsx
import React from "react";
import { createSession, Format } from "react-forge";
import { Presentation, Slide, Column, Text } from "react-forge/pptx";

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

Import React in TSX tasks when using the classic JSX transform. The bundled `tsx` loader also respects applicable caller TypeScript configuration. CLI task data is inline JSON: `--data '{"title":"Example"}'`. Use `--json` for a structured result. Caller code executes with the caller's permissions; React Forge is not a sandbox.

Library callers explicitly dispose their sessions in `finally`. A session supports `render`, `registerImage`, `inspect`, `mount`, `measure`, `exportBuffer`, `exportFile`, `subscribe`, and `dispose`. `importOffice(Format.Pptx, bytesOrPath)` returns an imported session; inspect its document-scoped targets and mount one React subtree into a supported target. A mounted region exposes `render` and `unmount`. Overlapping targets are rejected. Imported opaque content remains untouched, and unsupported edits fail explicitly.

Export waits for render-relevant Suspense work and registered assets, then pins the current model. Later state updates cannot change that output. `useLayoutEffect` observes the React commit, not native layout completion. Use `measure(handle, { revision })` for asynchronous geometry. Effects that initiate unrelated asynchronous work remain the caller's responsibility.

Pass `AbortSignal` in export/import/asset options. There is no automatic timeout. SIGINT/SIGTERM cancel a CLI run and dispose its returned session. Default file export rejects an existing destination; `overwrite: true` (CLI `--overwrite`) enables atomic replacement. Replacing an imported source first checks its fingerprint. Correct changed/invalid input and retry; no automatic recovery store or revision archive exists. Disposal never deletes exported files.

`ForgeError` provides a stable `ErrorCode`. Structured diagnostic subscribers receive stage, format, revision, duration and error classification; observer failures do not change operation outcomes. Document text, asset bytes, XML, host paths and credentials are excluded from diagnostic events. A failure after file publication explicitly reports `published: true`.

The exported `limits` object describes byte, ZIP, XML, image and rendered-tree budgets. Refer to the canonical internal contracts in `docs/project-react-forge.md` and its linked requirements for the full capability and preservation boundaries. Existing Forge consumers retain their existing interfaces and bundled default font.
