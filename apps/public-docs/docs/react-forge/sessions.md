# Sessions and common API

`createSession(Format.Pptx | Format.Docx | Format.Xlsx | Format.Pdf)` creates an in-memory document session. `render()` commits a React tree, including stateful components, effects, Suspense, and transitions. Use ordinary React updates to change a newly authored document. Sessions serialize their own mutations, and an invalid latest render fails export instead of silently exporting an older revision.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Document, Page, Paragraph } from "@delino/react-forge/pdf";

const session = createSession(Format.Pdf);
try {
  await session.render(
    <Document language="en-US">
      <Page><Paragraph heading={1}>Project report</Paragraph></Page>
    </Document>,
  );
  const inspection = await session.snapshot();
  console.log(inspection.revision);
  await session.exportFile("report.pdf");
} finally {
  await session.dispose();
}
```

`snapshot({ signal })` waits for relevant React/Suspense and registered asset work and returns a settled inspection without generating a file. `inspect()` reads the current known targets. `exportBuffer()` returns bytes; `exportFile(path, { overwrite, signal })` publishes a local file. Each export pins its own revision, so later React updates cannot alter an export already in progress. Independent asynchronous work started by an effect remains your responsibility.

## Fonts and assets

Use `await session.registerImage({ path: "image.png" })` or pass bytes before rendering an `Image` component. `registerFont({ path: "font.ttf" })` registers a caller font. System font discovery is the default; `createSession(format, { systemFonts: false })` uses caller-registered fonts only. Registered work settles before an export pins its revision. Office files reference font families, so the consuming Office environment also needs appropriate fonts. PDF embeds font subsets when the font license permits.

## Inspection and measurement

Refs expose typed node handles. After an export establishes a revision, call `measure(handle, { revision: session.revision })` for supported geometry; a stale revision fails. PPTX and PDF use page coordinates. DOCX authoring geometry is pre-pagination Word flow, while XLSX uses worksheet coordinates; Office applications decide final layout and pagination. Nodes with no meaningful geometry return `invalid_target`.

## Lifecycle and diagnostics

Pass an `AbortSignal` to operations that accept it; there is no automatic operation timeout. `dispose()` cancels and joins owned work. `subscribe(listener)` receives bounded operation diagnostics with stage, revision, duration, and stable classification; it does not include document contents, asset bytes, credentials, or host paths. `ForgeError.code`, `ErrorCode`, `limits`, and `capabilities` help classify failures. An in-memory session is not a durable revision archive. See [limits and troubleshooting](/react-forge/limits-and-troubleshooting).
