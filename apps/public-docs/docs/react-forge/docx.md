# Author DOCX documents

Use `@delino/react-forge/docx` with a `Format.Docx` session. `Document` contains `Section` elements; sections may have headers and footers. Paragraphs support headings, styled runs, and links. Lists, merged tables, images, explicit page breaks, and editable bar, line, and pie charts are available.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Document, Section, Paragraph, Run, Table, Row, Cell } from "@delino/react-forge/docx";

const session = createSession(Format.Docx);
try {
  await session.render(<Document language="en-US"><Section>
    <Paragraph heading={1}>Quarterly report</Paragraph>
    <Paragraph><Run style={{ bold: true }}>Summary:</Run> revenue rose.</Paragraph>
    <Table columns={[220, 220]}>
      <Row header><Cell><Paragraph>Quarter</Paragraph></Cell><Cell><Paragraph>Value</Paragraph></Cell></Row>
      <Row><Cell><Paragraph>Q1</Paragraph></Cell><Cell><Paragraph>4</Paragraph></Cell></Row>
    </Table>
  </Section></Document>);
  await session.exportFile("report.docx");
} finally {
  await session.dispose();
}
```

For images, register PNG/JPEG bytes or an explicit local path, then pass the returned asset to `Image` with width, height, and alt text. Use `Chart` for native editable chart data. The [session guide](/react-forge/sessions#fonts-and-assets) describes font registration and settled assets.

React Forge reports authoring measurements before Word pagination. A mounted region can be measured using known source constraints; some source layouts have no reliable width and therefore no geometry, although safe export remains possible. Word controls final pagination and font substitution. For preservation-aware changes to an existing file, use [Office editing](/react-forge/office-editing); React Forge does not convert DOCX to PDF.
