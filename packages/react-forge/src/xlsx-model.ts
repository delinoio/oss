import { ForgeError } from "./errors.js";
import { ErrorCode } from "./types.js";
import { commonStyle } from "./docx-model.js";
import type { SerializedNode } from "./renderer.js";
import type { Address, CellFormat, ConditionalRule, Range, Value } from "./xlsx.js";
type Model = Record<string, unknown>;
const invalid = (): never => { throw new ForgeError(ErrorCode.MalformedInput, "Invalid spreadsheet component nesting or props."); };

function scalar(value: unknown): Model {
  if (typeof value === "string") return { type: "text", value };
  if (typeof value === "number") return { type: "number", value };
  if (typeof value === "boolean") return { type: "boolean", value };
  return invalid();
}
function valueModel(value: Value): Model {
  if (value === null) return { type: "blank" };
  if (typeof value !== "object") return scalar(value);
  if (value.type === "date") return value;
  if (value.type === "formula") return { type: "formula", value: { expression: value.expression, cached: value.cached === undefined ? undefined : scalar(value.cached) } };
  return invalid();
}
function formatModel(format?: CellFormat): Model | undefined {
  return format && { style: commonStyle(format.style), number_format: format.numberFormat, wrap: format.wrap, border: format.border };
}
function chart(node: SerializedNode): Model {
  const p = node.props;
  return { kind: p.kind, title: p.title, categories: p.categories, series: p.series, legend: p.legend, labels: p.labels };
}
function ruleModel(rule: ConditionalRule): Model {
  if (!rule || typeof rule !== "object") invalid();
  if (rule.type === "cell_value" || rule.type === "formula") return { ...rule, format: formatModel(rule.format) };
  if (rule.type === "data_bar" || rule.type === "icon_set") {
    const { hideValue, ...rest } = rule;
    return { ...rest, hide_value: hideValue };
  }
  return { ...rule };
}
export function xlsxEdit(nodes: SerializedNode[], region: { address?: Address; range?: Range }): Model {
  if (nodes.length !== 1) invalid();
  const node = nodes[0]!;
  if (node.children.length) invalid();
  const p = node.props;
  if (node.type === "xlsx:cell") return { type: "cell", value: { id: node.id, address: p.address ?? region.address, value: valueModel(p.value as Value), format: formatModel(p.format as CellFormat | undefined), hyperlink: p.hyperlink } };
  if (node.type === "xlsx:conditional-format") return { type: "conditional_format", value: { id: node.id, range: p.range ?? region.range, rule: ruleModel(p.rule as ConditionalRule) } };
  if (node.type === "xlsx:data-validation") return { type: "validation", value: { id: node.id, range: p.range ?? region.range, kind: p.kind, operator: p.operator, formulas: p.formulas, allow_blank: p.allowBlank, prompt: p.prompt, error: p.error } };
  if (node.type === "xlsx:chart") return { type: "chart", value: chart(node) };
  return invalid();
}
export function xlsxModel(nodes: SerializedNode[], documentId: string): Model {
  if (nodes.length !== 1 || nodes[0]?.type !== "xlsx:workbook") invalid();
  return { id: documentId, sheets: nodes[0]!.children.map(sheet => {
    if (sheet.type !== "xlsx:worksheet") invalid();
    const cells: unknown[] = [], merges: unknown[] = [], rows: unknown[] = [], columns: unknown[] = [], conditional_formats: unknown[] = [], validations: unknown[] = [], charts: unknown[] = [];
    for (const node of sheet.children) {
      if (node.children.length) invalid();
      const p = node.props;
      if (node.type === "xlsx:merge") merges.push(p.range);
      else if (node.type === "xlsx:row") rows.push({ row: p.row, height: p.height });
      else if (node.type === "xlsx:column") columns.push({ column: p.column, width: p.width });
      else if (node.type === "xlsx:chart") charts.push({ id: node.id, at: p.at ?? { row: 0, column: 0 }, width: p.width ?? 640, height: p.height ?? 360, alt: p.alt ?? p.title ?? "Chart", chart: chart(node) });
      else {
        const edit = xlsxEdit([node], {});
        if (edit.type === "cell") cells.push(edit.value);
        else if (edit.type === "conditional_format") conditional_formats.push(edit.value);
        else if (edit.type === "validation") validations.push(edit.value);
        else invalid();
      }
    }
    return { id: sheet.id, name: sheet.props.name, freeze: sheet.props.freeze, autofilter: sheet.props.autofilter, cells, merges, rows, columns, conditional_formats, validations, charts };
  }) };
}
