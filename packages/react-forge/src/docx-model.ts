import { ForgeError } from "./errors.js";
import { ErrorCode, type TextStyle } from "./types.js";
import type { SerializedNode } from "./renderer.js";
type Model = Record<string, unknown>;
const invalid = (): never => { throw new ForgeError(ErrorCode.MalformedInput, "Invalid Word component nesting or props."); };

export function commonStyle(value: unknown): Model {
  const s = (value ?? {}) as TextStyle;
  return { font_family: s.fontFamily, font_size: s.fontSize, bold: s.bold, italic: s.italic, underline: s.underline,
    color: s.color, background: s.background, language: s.language, direction: s.direction, align: s.align };
}

function runs(nodes: SerializedNode[]): Model[] {
  return nodes.map(node => {
    if (node.type === "#text") return { text: node.props.text };
    if (!["docx:run", "docx:link"].includes(node.type)) invalid();
    return { text: node.children.map(n => n.type === "#text" ? String(n.props.text) : invalid()).join(""),
      style: commonStyle(node.props.style), hyperlink: node.type === "docx:link" ? node.props.href : undefined };
  });
}

export function docxBlocks(nodes: SerializedNode[], documentId: string): Model[] {
  return nodes.flatMap(node => {
    const p = node.props;
    if (["docx:page-break", "docx:image", "docx:chart"].includes(node.type) && node.children.length) invalid();
    if (node.type === "docx:list") return node.children.map(item => {
      if (item.type !== "docx:list-item") invalid();
      return { type: "paragraph", id: item.id, style: commonStyle(item.props.style), list: { kind: p.kind ?? "bullet", level: p.level ?? 0 }, runs: runs(item.children) };
    });
    if (node.type === "docx:paragraph") return [{ type: "paragraph", id: node.id, style: commonStyle(p.style), heading: p.heading, runs: runs(node.children) }];
    if (node.type === "docx:page-break") return [{ type: "page_break", id: node.id }];
    if (node.type === "docx:image") {
      const asset = p.asset as { documentId?: string; assetId?: string } | undefined;
      if (!asset?.assetId || asset.documentId !== documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Image asset belongs to another document.");
      return [{ type: "image", id: node.id, asset: asset.assetId, width: p.width, height: p.height, alt: p.alt }];
    }
    if (node.type === "docx:chart") return [{ type: "chart", id: node.id, width: p.width, height: p.height, alt: p.alt,
      chart: { kind: p.kind, title: p.title, categories: p.categories, series: p.series, legend: p.legend, labels: p.labels } }];
    if (node.type === "docx:table") return [{ type: "table", id: node.id, columns: p.columns, rows: node.children.map(row => {
      if (row.type !== "docx:row") invalid();
      return { header: row.props.header, cells: row.children.map(cell => {
        if (cell.type !== "docx:cell") invalid();
        return { row_span: cell.props.rowSpan, col_span: cell.props.colSpan, style: commonStyle(cell.props.style), blocks: docxBlocks(cell.children, documentId) };
      }) };
    }) }];
    return invalid();
  });
}

export function docxModel(nodes: SerializedNode[], documentId: string): Model {
  if (nodes.length !== 1 || nodes[0]?.type !== "docx:document") invalid();
  const root = nodes[0]!;
  return { id: documentId, language: root.props.language, sections: root.children.map(section => {
    if (section.type !== "docx:section") invalid();
    const headers = section.children.filter(n => n.type === "docx:header");
    const footers = section.children.filter(n => n.type === "docx:footer");
    if (headers.length > 1 || footers.length > 1) invalid();
    return { width: section.props.width, height: section.props.height, margin: section.props.margin,
      header: docxBlocks(headers[0]?.children ?? [], documentId), footer: docxBlocks(footers[0]?.children ?? [], documentId),
      blocks: docxBlocks(section.children.filter(n => !["docx:header", "docx:footer"].includes(n.type)), documentId) };
  }) };
}
