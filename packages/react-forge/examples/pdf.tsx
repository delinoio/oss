import React from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format } from "react-forge";
import { Document, Page, Paragraph, Run, Link, List, ListItem, Table, Row, Cell, Image, Shape } from "react-forge/pdf";

export default async function task({ signal }: { signal: AbortSignal }) {
  const session = createSession(Format.Pdf);
  const image = await session.registerImage({ path: fileURLToPath(new URL("./sample.png", import.meta.url)) }, { signal });
  await session.render(<Document language="en-US" title="React Forge native PDF"><Page width={612} height={792} margin={48}>
    <Paragraph heading={1}>Native PDF report</Paragraph>
    <Paragraph><Run style={{ bold: true }}>Independent authoring</Run> with flow layout and semantic reading order.</Paragraph>
    <Paragraph>Latin · 한국어 · 日本語 · 中文 · 😀</Paragraph>
    <Paragraph style={{ direction: "rtl", language: "ar" }}>مرحبا بالعالم — Hello 123</Paragraph>
    <List ordered><ListItem>Automatic paragraph and table pagination.</ListItem><ListItem>Repeated table headers remain visual artifacts.</ListItem></List>
    <Image asset={image} width={160} height={80} alt="Generated blue test pattern" />
    <Shape kind="line" width={500} height={1} stroke="#888888" />
    <Table columns={[100, 416]}><Row header><Cell>Record</Cell><Cell>Description</Cell></Row>
      {Array.from({ length: 28 }, (_, index) => <Row key={index}><Cell>{String(index + 1)}</Cell><Cell>{"This record demonstrates a table flowing across page boundaries. ".repeat(index % 4 + 1)}</Cell></Row>)}
    </Table>
    <Paragraph><Link href="https://example.com">Accessible reference link</Link></Paragraph>
  </Page></Document>);
  return session;
}
