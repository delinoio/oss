import { ForgeError } from "./errors.js";
import { ErrorCode, type AssetHandle } from "./types.js";
import type { SerializedNode } from "./renderer.js";
type Model = Record<string, unknown>;
const invalid = (): never => { throw new ForgeError(ErrorCode.MalformedInput, "Invalid sprite component nesting or props."); };

function props(node: SerializedNode, names: string[], leaf = false) {
  const allowed = new Set(["children", "ref", ...names]);
  if (Object.keys(node.props).some(key => !allowed.has(key)) || leaf && node.children.length) invalid();
  // JSON turns non-finite numbers into null, which could silently select an
  // optional native default. Reject them before the model crosses the bridge.
  for (const value of Object.values(node.props)) {
    if (value === null || typeof value === "number" && !Number.isFinite(value)) invalid();
  }
  return node.props;
}
function point(value: unknown, fields: string[]): unknown {
  if (value === undefined) return undefined;
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid();
  const entries = Object.entries(value);
  if (entries.length !== fields.length || entries.some(([key, value]) => !fields.includes(key) || typeof value !== "number" || !Number.isFinite(value))) invalid();
  return value;
}
function drawings(nodes: SerializedNode[], documentId: string): Model[] {
  return nodes.map(node => {
    const type = node.type;
    let content: Model;
    if (type === "sprite:layer") {
      const p = props(node, ["x", "y", "visible"]);
      content = { type: "layer", visible: p.visible ?? true, children: drawings(node.children, documentId) };
    } else if (["sprite:pixel", "sprite:rect", "sprite:ellipse"].includes(type)) {
      const pixel = type === "sprite:pixel";
      const p = props(node, ["x", "y", "fill", ...pixel ? [] : ["width", "height"]], true);
      content = { type: type === "sprite:ellipse" ? "ellipse" : "rect", width: pixel ? 1 : p.width, height: pixel ? 1 : p.height, fill: p.fill };
    } else if (type === "sprite:pixel-grid") {
      const p = props(node, ["x", "y", "rows"], true);
      content = { type: "pixel_grid", rows: p.rows };
    } else if (type === "sprite:image") {
      const p = props(node, ["x", "y", "width", "height", "asset", "source", "flipX", "flipY"], true);
      const asset = p.asset as AssetHandle | undefined;
      if (!asset || asset.documentId !== documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Sprite image belongs to another session.");
      content = { type: "image", asset: asset.assetId, width: p.width, height: p.height, source: point(p.source, ["x", "y", "width", "height"]), flip_x: p.flipX ?? false, flip_y: p.flipY ?? false };
    } else return invalid();
    return { id: node.id, x: node.props.x ?? 0, y: node.props.y ?? 0, ...content };
  });
}
export function spriteModel(nodes: SerializedNode[], documentId: string): Model {
  if (nodes.length !== 1 || nodes[0]?.type !== "sprite:project") invalid();
  const root = nodes[0]!;
  const p = props(root, ["width", "height", "scale", "columns", "padding", "palette"]);
  return { id: documentId, width: p.width, height: p.height, scale: p.scale ?? 1, columns: p.columns, padding: p.padding ?? 1, palette: p.palette ?? {}, animations: root.children.map(animation => {
    if (animation.type !== "sprite:animation") invalid();
    const a = props(animation, ["name", "loop"]);
    return { id: animation.id, name: a.name, loop: a.loop ?? true, frames: animation.children.map(frame => {
      if (frame.type !== "sprite:frame") invalid();
      const f = props(frame, ["durationMs", "pivot"]);
      return { id: frame.id, duration_ms: f.durationMs, pivot: point(f.pivot, ["x", "y"]), children: drawings(frame.children, documentId) };
    }) };
  }) };
}
