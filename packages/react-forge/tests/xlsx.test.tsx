import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import React, { createRef } from "react";
import { createSession, importOffice, Format } from "../src/index.js";
import { Workbook, Worksheet, Cell, Merge, Row, Column, Chart, ConditionalFormat, DataValidation, Comparison, ValidationKind } from "../src/xlsx.js";
const a = (row: number, column = 0) => ({ row, column });
const range = { first: a(1), last: a(10) };

test("React creates native spreadsheet values, rules, charts and multiple sheets", async () => {
  const session = createSession(Format.Xlsx);
  const cell = createRef<import("../src/index.js").NodeHandle>();
  try {
    await session.render(<Workbook><Worksheet name="Report" freeze={a(1)} autofilter={range}>
      <Cell address={a(0)} value="React workbook" format={{ style: { bold: true }, wrap: true }} />
      <Cell ref={cell} address={a(1)} value={42} /><Cell address={a(2)} value={true} />
      <Cell address={a(3)} value={{ type: "date", value: "2026-09-24" }} />
      <Cell address={a(4)} value={{ type: "formula", expression: "A2*2", cached: 84 }} />
      <Cell address={a(5)} value="Link" hyperlink="https://example.com" />
      <Merge range={{ first: a(0), last: a(0, 1) }} /><Row row={0} height={30} /><Column column={0} width={24} />
      <ConditionalFormat range={range} rule={{ type: "cell_value", operator: Comparison.GreaterThan, values: ["10"], format: { style: { background: "#FF0000" } } }} />
      <DataValidation range={range} kind={ValidationKind.Integer} operator={Comparison.Between} formulas={["0", "100"]} />
      <Chart kind="line" categories={["A", "B"]} series={[{ name: "Revenue", values: [2, 4] }]} />
    </Worksheet><Worksheet name="Other"><Cell address={a(0)} value="Unrelated" /></Worksheet></Workbook>);
    const imported = await importOffice(Format.Xlsx, await session.exportBuffer());
    const box = await session.measure(cell.current!, { revision: session.revision });
    assert.equal(box.coordinateSpace, "worksheet"); assert.equal(box.y, 30); assert.equal(box.height, 15);
    try {
      const targets = imported.inspect().targets;
      for (const kind of ["cell", "conditional_format", "validation", "chart"]) assert.ok(targets.some(t => t.kind === kind));
      assert.ok(targets.some(t => t.sheet === "Other" && t.text === "Unrelated"));
      const target = targets.find(t => t.sheet === "Report" && t.address?.row === 1 && t.address.column === 0)!;
      await imported.mount(target, <Cell value={99} />);
      const updated = await importOffice(Format.Xlsx, await imported.exportBuffer());
      try { assert.ok(updated.inspect().targets.some(t => t.sheet === "Report" && t.text === "99")); }
      finally { await updated.dispose(); }
    } finally { await imported.dispose(); }
  } finally { await session.dispose(); }
});

test("externally authored spreadsheet mounts retain region addresses and support chart replacement", async () => {
  const bytes = await readFile(new URL("../../../crates/forge-xlsx/tests/fixtures/external.xlsx", import.meta.url));
  const session = await importOffice(Format.Xlsx, bytes);
  try {
    const target = session.inspect().targets.find(t => t.kind === "cell")!;
    const mount = await session.mount(target, <Cell value="Replaced" />);
    const chart = session.inspect().targets.find(t => t.kind === "chart")!;
    await session.mount(chart, <Chart kind="bar" categories={["New"]} series={[{ name: "Series", values: [7] }]} />);
    const output = await importOffice(Format.Xlsx, await session.exportBuffer());
    try { assert.ok(output.inspect().targets.some(t => t.text === "Replaced")); assert.equal(output.inspect().targets.filter(t => t.kind === "chart").length, 3); }
    finally { await output.dispose(); }
    await mount.unmount();
    assert.ok((await session.exportBuffer()).length);
  } finally { await session.dispose(); }
});
