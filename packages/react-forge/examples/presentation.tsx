import React from "react";
import { createSession, Format } from "react-forge";
import { Presentation, Slide, Column, Text, Table, TableRow, Cell, Chart } from "react-forge/pptx";

export default async function task({ data, signal }: { data?: { title?: string }; signal: AbortSignal }) {
  signal.throwIfAborted();
  const session = createSession(Format.Pptx);
  await session.render(<Presentation><Slide><Column padding={32} gap={18}>
    <Text style={{ fontSize: 32, bold: true }}>{data?.title ?? "React Forge"}</Text>
    <Text>Reusable components and persistent React state.</Text>
    <Table height={100} columns={["fill", "fill"]}>
      <TableRow><Cell>Format</Cell><Cell>Editable</Cell></TableRow>
      <TableRow><Cell>PPTX</Cell><Cell>Yes</Cell></TableRow>
    </Table>
    <Chart height={200} legend="bottom" dataLabels="value"
      data={{ categories: ["A", "B"], series: [{ key: "example", name: "Example", values: [4, 7] }] }} />
  </Column></Slide></Presentation>);
  return session;
}
