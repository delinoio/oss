# Edit existing Office files

`importOffice()` accepts PPTX, DOCX, or XLSX bytes or an explicit local path. Inspect the imported file, choose a supported editable target, mount React content into that region, and export to a **different** path. Unselected Office package parts and XML remain preserved; unsupported regions remain opaque instead of being flattened.

Use this flow for a targeted change to an existing file. For a new document, start with [Getting started](/react-forge/getting-started) and its format guide.

## Choose a target and export

```tsx
import React from "react";
import { importOffice, Format } from "@delino/react-forge";
import { Paragraph } from "@delino/react-forge/docx";

const session = await importOffice(Format.Docx, { path: "original.docx" });
try {
  const target = session.inspect().targets.find(
    (item) => item.kind === "paragraph" && item.editable,
  );
  if (!target) throw new Error("No supported paragraph was found.");
  const region = await session.mount(target, <Paragraph>Updated content</Paragraph>);
  await region.render(<Paragraph>Revised content</Paragraph>);
  await session.exportFile("edited.docx");
} finally {
  await session.dispose();
}
```

## Preservation and output conflicts

Targets are session-scoped handles; do not reconstruct one from a serialized ID. Mounted regions cannot overlap. `region.unmount()` relinquishes that React region and restores its original imported model in the current session. It does not change an already exported file. New documents use root `session.render()`; imported documents use mounted regions. An edit that cannot prove preservation fails rather than publishing a partial replacement.

Existing output files fail by default. `overwrite: true` allows replacing a separate destination atomically, but the imported source and its canonical path aliases can **never** be replaced. If the source changes externally during editing, the operation fails; choose a new import from the current source and retry. A concurrent external save after the fingerprint check cannot be ruled out on every filesystem, so coordinate saves when multiple applications use the same file.

Encrypted, signed, macro-enabled, Strict, and legacy Office packages are unsupported. PDF import/editing is not available. See [sessions](/react-forge/sessions), [CLI](/react-forge/cli), and [limits and troubleshooting](/react-forge/limits-and-troubleshooting).
