# Author tagged PDF

React Forge builds PDF from its own `@delino/react-forge/pdf` model. It does not import PDF or convert an Office file. `Document` and `Page` contain flow text, lists, tables, images, shapes, links, and explicit page breaks; paragraphs and tables paginate automatically.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Document, Page, Paragraph, Table, Row, Cell } from "@delino/react-forge/pdf";

const session = createSession(Format.Pdf);
try {
  await session.render(<Document language="en-US" title="Quarterly report">
    <Page width={612} height={792} margin={48}>
      <Paragraph heading={1}>Quarterly report</Paragraph>
      <Table columns={[150, 350]}>
        <Row header><Cell>Quarter</Cell><Cell>Revenue</Cell></Row>
        <Row><Cell>Q1</Cell><Cell>4</Cell></Row>
      </Table>
    </Page>
  </Document>);
  await session.exportFile("report.pdf");
} finally {
  await session.dispose();
}
```

Semantic output includes heading and paragraph structure, lists, table headers and cells, links, image alternative text, language, and reading order. Repeated table headers are visual copies with one logical header. Add alt text to informative images; decorative shapes can omit it. Large indivisible content can fail with `layout_overflow` instead of spilling beyond a page.

System or registered fonts must cover your text. PDF embeds subsets when the font permits it; restricted or unsupported embedding fails explicitly. CJK, mixed direction, RTL, and color emoji depend on suitable font coverage. React Forge does **not** claim PDF/UA certification. See [sessions](/react-forge/sessions) for font and export behavior and [releases](/react-forge/releases) for validation limits.
