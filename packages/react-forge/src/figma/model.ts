import { ForgeError } from "../errors.js";
import { ErrorCode, type AssetHandle } from "../types.js";
import type { SerializedNode } from "../renderer.js";
export enum FigmaKind {
  Page = "PAGE",
  Frame = "FRAME",
  Text = "TEXT",
  Rectangle = "RECTANGLE",
  Ellipse = "ELLIPSE",
  Line = "LINE",
  Vector = "VECTOR",
  Component = "COMPONENT",
  ComponentSet = "COMPONENT_SET",
  Instance = "INSTANCE",
  Collection = "COLLECTION",
  Variable = "VARIABLE",
  PaintStyle = "PAINT_STYLE",
  TextStyle = "TEXT_STYLE",
}
export interface Entity {
  key: string;
  kind: FigmaKind;
  parent: string | null;
  page: string | null;
  props: Record<string, any>;
  children: string[];
}
export interface Operation {
  action: "create" | "update" | "delete";
  entity: Entity;
  previous: Entity | null;
}
export interface Batch {
  page: string | null;
  operations: Operation[];
}
export interface Plan {
  batches: Batch[];
  operationCount: number;
}
export interface Snapshot {
  stateHash?: string;
  id: string;
  type: string;
  parent: string | null;
  children: string[];
  props: Record<string, any>;
  bounds?: { x: number; y: number; width: number; height: number };
}
export const resourceKinds = new Set([
  FigmaKind.Collection,
  FigmaKind.Variable,
  FigmaKind.PaintStyle,
  FigmaKind.TextStyle,
]);
const kindMap: Record<string, FigmaKind> = {
  page: FigmaKind.Page,
  frame: FigmaKind.Frame,
  text: FigmaKind.Text,
  rectangle: FigmaKind.Rectangle,
  ellipse: FigmaKind.Ellipse,
  line: FigmaKind.Line,
  vector: FigmaKind.Vector,
  image: FigmaKind.Rectangle,
  component: FigmaKind.Component,
  "component-set": FigmaKind.ComponentSet,
  instance: FigmaKind.Instance,
  collection: FigmaKind.Collection,
  variable: FigmaKind.Variable,
  "paint-style": FigmaKind.PaintStyle,
  "text-style": FigmaKind.TextStyle,
};
export function rgb(hex: string) {
  if (!/^#[0-9a-f]{6}$/i.test(hex))
    throw new ForgeError(
      ErrorCode.MalformedInput,
      "Figma colors require #RRGGBB.",
    );
  return {
    r: parseInt(hex.slice(1, 3), 16) / 255,
    g: parseInt(hex.slice(3, 5), 16) / 255,
    b: parseInt(hex.slice(5, 7), 16) / 255,
  };
}
export const solid = (hex: string) => [{ type: "SOLID", color: rgb(hex) }];
export function model(
  nodes: SerializedNode[],
  documentId: string,
  bindings: Record<string, string>,
  parent: string | null = null,
  page: string | null = null,
  selected?: string,
): Entity[] {
  const output: Entity[] = [];
  const aliases = new Map<string, string>();
  const gather = (n: SerializedNode) => {
    if (typeof n.props.nodeKey === "string") {
      if (aliases.has(n.props.nodeKey))
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Duplicate Figma nodeKey.",
        );
      aliases.set(n.props.nodeKey, n.id);
    }
    n.children.forEach(gather);
  };
  nodes.forEach(gather);
  const ref = (value: any): string => {
    if (typeof value !== "string")
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Figma references require a nodeKey or @remote-ID.",
      );
    return aliases.get(value) ?? value;
  };
  const text = (n: SerializedNode): string =>
    n.type === "#text" ? String(n.props.text) : n.children.map(text).join("");
  const walk = (
    node: SerializedNode,
    parent: string | null,
    page: string | null,
    forced?: string,
  ): string[] => {
    if (node.type === "figma:document")
      return node.children.flatMap((n) => walk(n, parent, page));
    const kind = kindMap[node.type.replace(/^figma:/, "")];
    if (!kind || !node.type.startsWith("figma:"))
      throw new ForgeError(
        ErrorCode.MalformedInput,
        "Unsupported Figma React element.",
      );
    const key = forced ?? node.id;
    const props: Record<string, any> = {};
    for (const [k, v] of Object.entries(node.props))
      if (
        ![
          "children",
          "ref",
          "nodeKey",
          "target",
          "asset",
          "fill",
          "stroke",
          "color",
          "padding",
          "gap",
        ].includes(k) &&
        v !== undefined
      )
        props[k] = structuredClone(v);
    if (node.props.fill !== undefined)
      props.fills = solid(String(node.props.fill));
    if (node.props.color !== undefined)
      props.fills = solid(String(node.props.color));
    if (node.props.stroke !== undefined)
      props.strokes = solid(String(node.props.stroke));
    if (node.props.gap !== undefined) props.itemSpacing = node.props.gap;
    if (node.props.padding !== undefined)
      for (const k of [
        "paddingTop",
        "paddingRight",
        "paddingBottom",
        "paddingLeft",
      ])
        props[k] = node.props.padding;
    if (kind === FigmaKind.Text) {
      props.characters = text(node);
      if (!forced && !node.props.target) {
        props.fontName ??= { family: "Inter", style: "Regular" };
        props.fontSize ??= 14;
      }
    }
    if (node.props.asset) {
      const asset = node.props.asset as AssetHandle;
      if (asset.documentId !== documentId)
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Image belongs to another session.",
        );
      props.image = asset.assetId;
      props.imageScaleMode ??= "FILL";
    }
    if (node.props.target) {
      if (
        typeof node.props.target !== "string" ||
        (bindings[key] && bindings[key] !== node.props.target)
      )
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Invalid explicit Figma target.",
        );
      bindings[key] = node.props.target;
    }
    if (kind === FigmaKind.Page) page = key;
    if (resourceKinds.has(kind)) {
      parent = null;
      page = null;
    }
    const entity: Entity = {
      key,
      kind,
      parent,
      page: kind === FigmaKind.Page ? null : page,
      props,
      children: [],
    };
    if (kind === FigmaKind.ComponentSet) {
      if (
        !node.children.length ||
        node.children.some((c) => c.type !== "figma:component")
      )
        throw new ForgeError(
          ErrorCode.MalformedInput,
          "ComponentSet requires Component variants.",
        );
      props.variants = node.children.flatMap((child) =>
        walk(child, parent, page),
      );
      output.push(entity);
    } else {
      output.push(entity);
      if (kind !== FigmaKind.Text)
        entity.children = node.children.flatMap((child) =>
          walk(child, key, page),
        );
    }
    return resourceKinds.has(kind) ? [] : [key];
  };
  nodes.flatMap((n) => walk(n, parent, page, selected));
  for (const entity of output) {
    for (const key of ["component", "collection", "fillStyle", "textStyle"])
      if (entity.props[key]) entity.props[key] = ref(entity.props[key]);
    if (entity.props.bindings)
      entity.props.bindings = Object.fromEntries(
        Object.entries(entity.props.bindings).map(([k, v]) => [k, ref(v)]),
      );
    if (
      entity.kind === FigmaKind.Variable &&
      entity.props.resolvedType === "COLOR" &&
      typeof entity.props.value === "string"
    )
      entity.props.value = rgb(entity.props.value);
  }
  return output;
}
