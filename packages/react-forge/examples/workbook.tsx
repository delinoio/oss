import React from "react";
import { createSession, Format } from "react-forge";
import { Workbook, Worksheet, Cell, Merge, Row, Column, Chart, ConditionalFormat, DataValidation, Comparison, ThresholdKind, IconSet, ValidationKind } from "react-forge/xlsx";
const at = (row: number, column = 0) => ({ row, column });
const range = (first: number, last: number, column = 1) => ({ first: at(first, column), last: at(last, column) });

export default async function task({ signal }: { signal: AbortSignal }) {
  signal.throwIfAborted();
  const session = createSession(Format.Xlsx);
  await session.render(<Workbook><Worksheet name="Report" freeze={at(2)} autofilter={range(1, 5, 0)}>
    <Cell address={at(0)} value="React Forge workbook" format={{ style: { bold: true, fontSize: 18 } }} /><Merge range={{ first: at(0), last: at(0, 3) }} />
    <Row row={0} height={30} /><Column column={0} width={24} /><Column column={1} width={18} />
    <Cell address={at(1)} value="Quarter" /><Cell address={at(1, 1)} value="Revenue" />
    {[4, 7, 9, 12].map((value, i) => <React.Fragment key={i}><Cell address={at(i + 2)} value={`Q${i + 1}`} /><Cell address={at(i + 2, 1)} value={value} /></React.Fragment>)}
    <Cell address={at(6)} value="Caller-supplied cache" /><Cell address={at(6, 1)} value={{ type: "formula", expression: "SUM(B3:B6)", cached: 32 }} />
    <Cell address={at(7)} value={{ type: "date", value: "2026-09-24" }} format={{ numberFormat: "yyyy-mm-dd" }} />
    <Cell address={at(8)} value={true} /><Cell address={at(9)} value="Reference" hyperlink="https://example.com" />
    <ConditionalFormat range={range(2, 5)} rule={{ type: "cell_value", operator: Comparison.GreaterThan, values: ["8"], format: { style: { color: "#008800" } } }} />
    <ConditionalFormat range={range(2, 5)} rule={{ type: "formula", formula: "B3>10", format: { style: { bold: true } } }} />
    <ConditionalFormat range={range(2, 5)} rule={{ type: "color_scale", thresholds: [{ kind: ThresholdKind.Minimum }, { kind: ThresholdKind.Maximum }], colors: ["#FFFFFF", "#3388FF"] }} />
    <ConditionalFormat range={range(2, 5)} rule={{ type: "data_bar", minimum: { kind: ThresholdKind.Minimum }, maximum: { kind: ThresholdKind.Maximum }, color: "#1188AA" }} />
    <ConditionalFormat range={range(2, 5)} rule={{ type: "icon_set", icons: IconSet.ThreeArrows, thresholds: [0, 33, 67].map(value => ({ kind: ThresholdKind.Percent, value: String(value) })) }} />
    {Object.values(ValidationKind).map((kind, i) => <DataValidation key={kind} range={range(i + 12, i + 12, 0)} kind={kind}
      operator={kind === ValidationKind.List || kind === ValidationKind.Custom ? undefined : Comparison.Between}
      formulas={kind === ValidationKind.List ? ['"One,Two,Three"'] : kind === ValidationKind.Custom ? ["LEN(A19)>0"] : kind === ValidationKind.Date ? ["1", "60000"] : ["0", "100"]}
      allowBlank prompt={`Enter ${kind}`} error="The entered value is outside the allowed range." />)}
    {(["bar", "line", "pie"] as const).map((kind, i) => <Chart key={kind} kind={kind} at={at(22 + i * 16)} width={560} height={280}
      title={`${kind} revenue`} alt={`${kind} revenue chart`} categories={["Q1", "Q2", "Q3", "Q4"]} series={[{ name: "Revenue", values: [4, 7, 9, 12] }]} legend labels />)}
  </Worksheet><Worksheet name="Notes"><Cell address={at(0)} value="Formulas are recalculated by the spreadsheet application." /></Worksheet></Workbook>);
  return session;
}
