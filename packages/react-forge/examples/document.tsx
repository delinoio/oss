import React from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format } from "@delino/react-forge";
import { Document, Section, Header, Footer, Paragraph, Run, Link, List, ListItem, Table, Row, Cell, Image, Chart, PageBreak } from "@delino/react-forge/docx";

export default async function task({ signal }: { signal: AbortSignal }) {
  const session = createSession(Format.Docx);
  const image = await session.registerImage({ path: fileURLToPath(new URL("./sample.png", import.meta.url)) }, { signal });
  await session.render(<Document language="en-US"><Section>
    <Header><Paragraph>React Forge — native Word report</Paragraph></Header>
    <Paragraph heading={1}>Quarterly report</Paragraph>
    <Paragraph><Run style={{ bold: true }}>Rich text</Run> with <Link href="https://example.com">an accessible link</Link>.</Paragraph>
    <Paragraph style={{ language: "ko" }}>한국어 · 日本語 · 中文</Paragraph>
    <Paragraph style={{ direction: "rtl", language: "ar" }}>مرحبا بالعالم</Paragraph>
    <List kind="number"><ListItem>Review native data.</ListItem><ListItem>Reuse React components.</ListItem></List>
    <Table columns={[234, 234]}>
      <Row header><Cell colSpan={2}><Paragraph>Merged table header</Paragraph></Cell></Row>
      <Row><Cell rowSpan={2}><Paragraph>Two rows</Paragraph></Cell><Cell><Paragraph>First value</Paragraph></Cell></Row>
      <Row><Cell><Paragraph>Second value</Paragraph></Cell></Row>
    </Table>
    <Image asset={image} width={180} height={90} alt="Generated test pattern" />
    <PageBreak />
    {(["bar", "line", "pie"] as const).map(kind => <Chart key={kind} kind={kind} title={`${kind} chart`} categories={["Q1", "Q2", "Q3"]}
      series={[{ name: "Revenue", values: [4, 7, 9] }]} width={360} height={180} alt={`${kind} revenue chart`} legend labels />)}
    <Footer><Paragraph>Editable Office elements; final pagination belongs to Word.</Paragraph></Footer>
  </Section></Document>);
  return session;
}
