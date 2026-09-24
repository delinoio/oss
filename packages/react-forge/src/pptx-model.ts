import { ForgeError } from "./errors.js";
import { ErrorCode, type TextStyle } from "./types.js";
import type { SerializedNode } from "./renderer.js";

type Model = Record<string, unknown>;
function invalid(): never { throw new ForgeError(ErrorCode.MalformedInput, "Invalid presentation component nesting or props."); }

function style(value: unknown): Model {
  if (!value) return {};
  const s = value as TextStyle;
  return { font_family: s.fontFamily, font_size: s.fontSize, font_weight: s.bold === undefined ? undefined : s.bold ? 700 : 400,
    color: s.color, italic: s.italic, underline: s.underline };
}

function text(nodes: SerializedNode[]): string {
  return nodes.map(n => n.type === "#text" ? String(n.props.text) : invalid()).join("");
}

function paragraphs(nodes: SerializedNode[]): Model {
  if (nodes.every(n => n.type === "#text")) return { text: text(nodes) };
  const paragraph = (children: SerializedNode[], align?: unknown) => ({ align: align ?? "left", runs: children.map(n => {
    if (n.type === "#text") return { text: n.props.text };
    if (n.type !== "pptx:run") invalid();
    return { text: text(n.children), style: style(n.props.style) };
  }) });
  if (nodes.some(n => n.type === "pptx:paragraph")) {
    return { paragraphs: nodes.map(n => n.type === "pptx:paragraph" ? paragraph(n.children, n.props.align) : invalid()) };
  }
  return { paragraphs: [paragraph(nodes)] };
}

export function pptxNode(n: SerializedNode, documentId: string): Model {
  const type = n.type.replace(/^pptx:/, "");
  if (!n.type.startsWith("pptx:") || !["row", "column", "canvas", "text", "list", "image", "shape", "table", "chart", "connector"].includes(type)) invalid();
  if (["list", "image", "shape", "chart", "connector"].includes(type) && n.children.length !== 0) invalid();
  const p = n.props;
  const node: Model = { id: n.id, type, key: p.nodeKey, frame: p.frame, width: p.width, height: p.height,
    padding: p.padding, gap: p.gap, style: style(p.style), overflow: p.overflow,
    min_font_size: p.minFontSize, placeholder_ref: p.placeholderRef };
  if (["row", "column", "canvas"].includes(type)) node.children = n.children.map(child => pptxNode(child, documentId));
  else if (type === "text") Object.assign(node, paragraphs(n.children));
  else if (type === "list") Object.assign(node, { items: p.items, marker: p.marker });
  else if (type === "image") {
    const asset = p.asset as { assetId?: string; documentId?: string } | undefined;
    if (!asset?.assetId || asset.documentId !== documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Image asset belongs to a different document.");
    Object.assign(node, { asset_ref: asset.assetId, alt: p.alt, fit: p.fit });
  } else if (type === "shape") Object.assign(node, { shape: p.shape, fill: p.fill ? { color: p.fill } : undefined });
  else if (type === "chart") Object.assign(node, { data: p.data, orientation: p.orientation, legend: p.legend, data_labels: p.dataLabels });
  else if (type === "connector") Object.assign(node, { from: p.from, to: p.to, connector_type: p.connectorType });
  else if (type === "table") {
    if (!Array.isArray(p.columns)) invalid();
    node.columns = p.columns.map(width => ({ width }));
    node.rows = n.children.map(row => {
      if (row.type !== "pptx:table-row") invalid();
      return { cells: row.children.map(cell => {
        if (cell.type !== "pptx:cell") invalid();
        return { ...paragraphs(cell.children), style: style(cell.props.style), row_span: cell.props.rowSpan,
          col_span: cell.props.colSpan, fill: cell.props.fill ? { color: cell.props.fill } : undefined };
      }) };
    });
  }
  return node;
}

export function pptxModel(nodes: SerializedNode[], documentId: string, assets: Map<string, Buffer>): Model {
  if (nodes.length !== 1 || nodes[0]?.type !== "pptx:presentation") invalid();
  const root = nodes[0];
  return { dsl_version: 1, kind: "presentation", page: { width: root.props.width ?? 960, height: root.props.height ?? 540 },
    theme: root.props.fontFamily ? { font_family: root.props.fontFamily } : undefined,
    assets: Object.fromEntries(Array.from(assets.keys(), id => [id, { handle: id }])),
    slides: root.children.map(slide => {
      if (slide.type !== "pptx:slide" || slide.children.length !== 1) invalid();
      return { id: slide.id, background: slide.props.background ? { color: slide.props.background } : undefined,
        slide_layout_ref: slide.props.layoutRef, content: pptxNode(slide.children[0]!, documentId) };
    }) };
}
