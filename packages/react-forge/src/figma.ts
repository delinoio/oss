import { createElement } from "react";
import type { AssetHandle, ElementProps } from "./types.js";
export { FigmaKind } from "./figma/model.js";
export { CredentialSource } from "./figma/credentials.js";
export {
  FigmaSession,
  openFigma,
  PublishStatus,
  FigmaPublishError,
} from "./figma/session.js";
export type {
  FigmaOptions,
  FigmaReceipt,
  FigmaTarget,
} from "./figma/session.js";
export interface FigmaProps extends ElementProps {
  nodeKey?: string;
  target?: string;
  name?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  rotation?: number;
  fills?: ReadonlyArray<{
    type: "SOLID";
    color: { r: number; g: number; b: number };
  }>;
  fill?: string;
  stroke?: string;
  strokeWeight?: number;
  opacity?: number;
  visible?: boolean;
  cornerRadius?: number;
  clipsContent?: boolean;
  layoutMode?: "NONE" | "HORIZONTAL" | "VERTICAL";
  layoutSizingHorizontal?: "FIXED" | "HUG" | "FILL";
  layoutSizingVertical?: "FIXED" | "HUG" | "FILL";
  primaryAxisSizingMode?: "AUTO" | "FIXED";
  counterAxisSizingMode?: "AUTO" | "FIXED";
  primaryAxisAlignItems?: "MIN" | "CENTER" | "MAX" | "SPACE_BETWEEN";
  counterAxisAlignItems?: "MIN" | "CENTER" | "MAX" | "BASELINE";
  padding?: number;
  paddingTop?: number;
  paddingBottom?: number;
  paddingLeft?: number;
  paddingRight?: number;
  gap?: number;
  bindings?: Record<string, string>;
  fillStyle?: string;
}
const component =
  <P extends ElementProps>(name: string) =>
  (props: P) =>
    createElement(`figma:${name}`, props);
export const Document = component<ElementProps>("document");
export const Page = component<
  ElementProps & { name: string; nodeKey?: string; target?: string }
>("page");
export const Frame = component<FigmaProps>("frame");
export const Rectangle = component<FigmaProps>("rectangle");
export const Ellipse = component<FigmaProps>("ellipse");
export const Line = component<FigmaProps>("line");
export const Vector = component<
  FigmaProps & {
    vectorPaths: { windingRule: "NONZERO" | "EVENODD"; data: string }[];
  }
>("vector");
export const Image = component<
  FigmaProps & { asset: AssetHandle; imageScaleMode?: "FILL" | "FIT" | "TILE" }
>("image");
export interface FigmaTextProps extends FigmaProps {
  color?: string;
  fontName?: { family: string; style: string };
  fontSize?: number;
  textAutoResize?: "NONE" | "HEIGHT" | "WIDTH_AND_HEIGHT" | "TRUNCATE";
  textAlignHorizontal?: "LEFT" | "CENTER" | "RIGHT" | "JUSTIFIED";
  lineHeight?: { unit: "AUTO" } | { unit: "PIXELS" | "PERCENT"; value: number };
  letterSpacing?: { unit: "PIXELS" | "PERCENT"; value: number };
  textStyle?: string;
}
export const Text = component<FigmaTextProps>("text");
export const Component = component<FigmaProps>("component");
export const ComponentSet = component<FigmaProps>("component-set");
export const Instance = component<
  FigmaProps & {
    component: string;
    componentProperties?: Record<string, string | boolean>;
  }
>("instance");
export const VariableCollection = component<
  ElementProps & { name: string; nodeKey: string }
>("collection");
export const Variable = component<
  ElementProps & {
    name: string;
    nodeKey: string;
    collection: string;
    resolvedType: "COLOR" | "FLOAT" | "STRING" | "BOOLEAN";
    value: string | number | boolean | { r: number; g: number; b: number };
    scopes: string[];
    codeSyntax?: { WEB?: string; ANDROID?: string; iOS?: string };
  }
>("variable");
export const PaintStyle = component<
  ElementProps & { name: string; nodeKey: string; fill: string }
>("paint-style");
export const TextStyle = component<
  Omit<FigmaTextProps, "children"> & { name: string; nodeKey: string }
>("text-style");
