import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import React, { createRef } from "react";
import { createSession, importOffice, Format, ErrorCode } from "../src/index.js";
import { Document, Section, Header, Footer, Paragraph, Run, Link, List, ListItem, Table, Row, Cell, Chart, PageBreak, Image } from "../src/docx.js";

test("React authors rich DOCX, sections, native charts and tables", async () => {
  const session = createSession(Format.Docx);
  let imported;
  const paragraph = createRef<import("../src/index.js").NodeHandle>();
  try {
    await session.render(<Document language="en-US"><Section>
      <Header><Paragraph>Header</Paragraph></Header>
      <Paragraph heading={1} ref={paragraph}><Run style={{ bold: true }}>Word from React</Run></Paragraph>
      <Paragraph><Link href="https://example.com">Link</Link></Paragraph>
      <List><ListItem>One</ListItem><ListItem>Two</ListItem></List>
      <Table columns={[150, 150]}><Row header><Cell colSpan={2}><Paragraph>Merged</Paragraph></Cell></Row></Table>
      <Chart kind="pie" categories={["A", "B"]} series={[{ name: "Values", values: [2, 3] }]} width={300} height={200} alt="Distribution" />
      <PageBreak />
      <Footer><Paragraph>Footer</Paragraph></Footer>
    </Section></Document>);
    const bytes = await session.exportBuffer();
    const box = await session.measure(paragraph.current!, { revision: session.revision });
    assert.equal(box.coordinateSpace, "word_flow"); assert.equal(box.width, 468); assert.ok(box.height > 0);
    assert.ok(session.inspect().targets.some(t => t.nodeId === paragraph.current!.nodeId));
    imported = await importOffice(Format.Docx, bytes);
    assert.ok(imported.inspect().targets.some(t => t.kind === "chart"));
    assert.ok(imported.inspect().targets.some(t => t.text === "Word from React"));
  } finally { await session.dispose(); await imported?.dispose(); }
});

test("external Word paragraph mounts survive repeated state updates", async () => {
  const bytes = await readFile(new URL("../../../crates/forge-docx/tests/fixtures/external.docx", import.meta.url));
  const session = await importOffice(Format.Docx, bytes);
  try {
    const target = session.inspect().targets.find(t => t.text === "External paragraph")!;
    const region = await session.mount(target, <Paragraph>React replacement</Paragraph>);
    const output = await session.exportBuffer();
    const reopened = await importOffice(Format.Docx, output);
    try { assert.ok(reopened.inspect().targets.some(t => t.text === "React replacement")); }
    finally { await reopened.dispose(); }
    await region.render(<Paragraph>Updated replacement</Paragraph>);
    assert.ok((await session.exportBuffer()).byteLength > 0);
  } finally { await session.dispose(); }
});


test("Word leaf components reject children without exporting an older valid revision", async () => {
  const session = createSession(Format.Docx);
  try {
    await session.render(<Document><Section><Paragraph>Valid baseline</Paragraph></Section></Document>);
    await session.exportBuffer();
    const leaves = [
      <PageBreak><Paragraph>Must not disappear</Paragraph></PageBreak>,
      <Image asset={{ documentId: session.documentId, assetId: "unused" }} width={20} height={20} alt="Example"><Paragraph>Must not disappear</Paragraph></Image>,
      <Chart kind="bar" categories={["A"]} series={[{ name: "Values", values: [1] }]} width={100} height={100} alt="Example"><Paragraph>Must not disappear</Paragraph></Chart>,
    ];
    for (const leaf of leaves) {
      await session.render(<Document><Section>{leaf}</Section></Document>);
      await assert.rejects(session.exportBuffer(), { code: ErrorCode.MalformedInput });
    }
    await session.render(<Document><Section><PageBreak /><Paragraph>Recovered</Paragraph></Section></Document>);
    assert.ok((await session.exportBuffer()).length > 0);
  } finally { await session.dispose(); }
});
