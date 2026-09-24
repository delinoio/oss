import { ForgeError } from "./errors.js";
import { ErrorCode, type AssetHandle } from "./types.js";
import { commonStyle } from "./docx-model.js";
import type { SerializedNode } from "./renderer.js";
type Model = Record<string, unknown>;
const invalid = (): never => { throw new ForgeError(ErrorCode.MalformedInput, "Invalid PDF component nesting or props."); };
function runs(nodes: SerializedNode[]): Model[] {
  return nodes.map(n => {
    if (n.type === "#text") return { text: n.props.text };
    if (!["pdf:run", "pdf:link"].includes(n.type)) invalid();
    return { text: n.children.map(c => c.type === "#text" ? c.props.text : invalid()).join(""), style: commonStyle(n.props.style), hyperlink: n.type === "pdf:link" ? n.props.href : undefined };
  });
}
function blocks(nodes: SerializedNode[], documentId: string): Model[] {
  return nodes.map(n => {
    const p = n.props;
    if (n.type === "pdf:paragraph") return { id: n.id, type: "paragraph", style: commonStyle(p.style), heading: p.heading, runs: runs(n.children) };
    if (n.type === "pdf:list") return { id: n.id, type: "list", ordered: p.ordered, items: n.children.map(item => item.type === "pdf:list-item" ? { id: item.id, style: commonStyle(item.props.style), runs: runs(item.children) } : invalid()) };
    if (n.type === "pdf:table") return { id: n.id, type: "table", columns: p.columns, rows: n.children.map(row => {
      if (row.type !== "pdf:row") invalid();
      return { id: row.id, header: row.props.header, cells: row.children.map(cell => cell.type === "pdf:cell" ? { id: cell.id, style: commonStyle(cell.props.style), runs: runs(cell.children) } : invalid()) };
    }) };
    if (n.children.length) invalid();
    if (n.type === "pdf:page-break") return { id: n.id, type: "page_break" };
    if (n.type === "pdf:image") {
      const asset = p.asset as AssetHandle | undefined;
      if (!asset || asset.documentId !== documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Image asset belongs to another document.");
      return { id: n.id, type: "image", asset: asset.assetId, width: p.width, height: p.height, alt: p.alt };
    }
    if (n.type === "pdf:shape") return { id: n.id, type: "shape", kind: p.kind, width: p.width, height: p.height, fill: p.fill, stroke: p.stroke, alt: p.alt };
    return invalid();
  });
}
export function pdfModel(nodes: SerializedNode[], documentId: string): Model {
  if (nodes.length !== 1 || nodes[0]?.type !== "pdf:document") invalid();
  const root = nodes[0]!;
  return { id: documentId, language: root.props.language, title: root.props.title, pages: root.children.map(page => {
    if (page.type !== "pdf:page") invalid();
    return { id: page.id, width: page.props.width ?? 612, height: page.props.height ?? 792, margin: page.props.margin ?? 36, blocks: blocks(page.children, documentId) };
  }) };
}
