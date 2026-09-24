import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import test from "node:test";
import React, { createRef } from "react";
import { createSession, Format, ErrorCode, type NodeHandle } from "../src/index.js";
import { Document, Page, Paragraph, Run, Link, List, ListItem, Table, Row, Cell, Shape } from "../src/pdf.js";

test("React PDF has independent authoring, pagination and revision geometry", async () => {
  const session = createSession(Format.Pdf);
  const ref = createRef<NodeHandle>();
  try {
    await session.render(<Document language="en-US" title="Report"><Page width={300} height={250} margin={20}>
      <Paragraph heading={1} ref={ref}>React PDF</Paragraph>
      <Paragraph><Run style={{ bold: true }}>Native</Run> text 한국어 مرحبا 😀</Paragraph>
      <List ordered><ListItem>First</ListItem><ListItem>Second</ListItem></List>
      <Table columns={[130, 130]}><Row header><Cell>Header</Cell><Cell>Value</Cell></Row>
        {Array.from({ length: 12 }, (_, i) => <Row key={i}><Cell>{`Row ${i}`}</Cell><Cell>{"Long text ".repeat(8)}</Cell></Row>)}
      </Table>
      <Paragraph><Link href="https://example.com">Link</Link></Paragraph>
      <Shape kind="rectangle" width={80} height={30} fill="#112233" alt="Rectangle" />
    </Page></Document>);
    const bytes = await session.exportBuffer();
    assert.equal(bytes.subarray(0, 5).toString(), "%PDF-");
    assert.ok(bytes.includes(Buffer.from("/StructTreeRoot")));
    const geometry = await session.measure(ref.current!, { revision: session.revision });
    assert.equal(geometry.page, 0); assert.ok(geometry.height > 0);
  } finally { await session.dispose(); }
});

test("caller font bytes work and missing glyphs fail with a typed error", async () => {
  const session = createSession(Format.Pdf, { systemFonts: false });
  try {
    await session.registerFont({ path: fileURLToPath(new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url)) });
    await session.render(<Document language="ko"><Page><Paragraph>한국어</Paragraph></Page></Document>);
    assert.ok((await session.exportBuffer()).length);
    await session.render(<Document language="en"><Page><Paragraph>{"\u{10ffff}"}</Paragraph></Page></Document>);
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.MissingFont });
    await session.render(<Document language="en"><Page><Paragraph>Recovered</Paragraph></Page></Document>);
    assert.ok((await session.exportBuffer()).length);
  } finally { await session.dispose(); }
});
