# Author PPTX presentations

Import components from `@delino/react-forge/pptx` and render them in a `Format.Pptx` session. `Presentation` contains `Slide` elements; `Row`, `Column`, and `Canvas` arrange content. `Text`, `Paragraph`, `Run`, and `List` provide text; `Image`, `Shape`, `Table`, `Chart`, and `Connector` add editable presentation elements.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Presentation, Slide, Column, Text, Chart } from "@delino/react-forge/pptx";

const session = createSession(Format.Pptx);
try {
  await session.render(<Presentation><Slide><Column padding={32} gap={18}>
    <Text style={{ fontSize: 32, bold: true }}>Results</Text>
    <Chart height={200} legend="bottom"
      data={{ categories: ["Q1", "Q2"], series: [
        { key: "revenue", name: "Revenue", values: [4, 7] },
      ] }} />
  </Column></Slide></Presentation>);
  await session.exportFile("results.pptx");
} finally {
  await session.dispose();
}
```

The chart retains editable workbook data. Tables support row and column spans. Images accept registered PNG/JPEG assets and require alternative text. Connector anchors refer to keyed targets. Text style supports font family, size, bold, italic, underline, and color; paragraph alignment belongs to `Paragraph.align`, and table-cell fill belongs to `Cell.fill`. Unknown style fields fail instead of being ignored.

Text, image, shape, chart, connector, and list elements are leaves: do not pass them nonempty React children unless the component's contract accepts text. Use `session.render()` again for new-document updates. To edit a supported region of an existing PPTX, see [Office editing](/react-forge/office-editing). Final font substitution and layout depend on the presentation application and available fonts. See [limits](/react-forge/limits-and-troubleshooting) for size ceilings.
