# Author XLSX workbooks

Use `@delino/react-forge/xlsx` in a `Format.Xlsx` session. `Workbook` contains named `Worksheet` elements. `Cell` accepts strings, numbers, Booleans, dates, and formulas; addresses and ranges are zero-based `{ row, column }` values. You can set dimensions, merges, freeze panes, filters, links, charts, conditional formats, and data validation.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Workbook, Worksheet, Cell, Chart } from "@delino/react-forge/xlsx";

const session = createSession(Format.Xlsx);
try {
  await session.render(<Workbook><Worksheet name="Report">
    <Cell address={{ row: 0, column: 0 }} value="Quarter" />
    <Cell address={{ row: 0, column: 1 }} value="Revenue" />
    <Cell address={{ row: 1, column: 0 }} value="Q1" />
    <Cell address={{ row: 1, column: 1 }} value={4} />
    <Cell address={{ row: 2, column: 1 }}
      value={{ type: "formula", expression: "B2*2", cached: 8 }} />
    <Chart kind="bar" at={{ row: 4, column: 0 }} width={400} height={180}
      categories={["Q1"]} series={[{ name: "Revenue", values: [4] }]} />
  </Worksheet></Workbook>);
  await session.exportFile("report.xlsx");
} finally {
  await session.dispose();
}
```

Formula cached values are optional and caller supplied; React Forge does not evaluate formulas. When a cache is absent, the workbook requests recalculation by the spreadsheet application. Bar, line, and pie charts retain editable data. Conditional formatting includes cell-value, formula, color-scale, data-bar, and icon-set rules; validation includes list, integer, decimal, date, time, text-length, and custom families.

Inspection reports sheet names and zero-based addresses/ranges. Worksheet geometry uses declared dimensions and a conventional column-width conversion; it is not a print-page or final font measurement. Use [Office editing](/react-forge/office-editing) for imported workbooks and [limits](/react-forge/limits-and-troubleshooting) for resource ceilings.
