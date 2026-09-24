import { createElement } from "react";
import type { AssetHandle, ElementProps, Frame, TextStyle } from "./types.js";

export type Size = number | "hug" | "fill";
export interface LayoutProps extends ElementProps {
  nodeKey?: string;
  frame?: Frame;
  width?: Size;
  height?: Size;
  padding?: number;
  gap?: number;
}
export interface TextProps extends LayoutProps {
  style?: TextStyle;
  overflow?: "error" | "shrink";
  minFontSize?: number;
  placeholderRef?: string;
}
const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`pptx:${name}`, props);

export const Presentation = component<ElementProps & { width?: number; height?: number; fontFamily?: string }>("presentation");
export const Slide = component<ElementProps & { background?: string; layoutRef?: string }>("slide");
export const Row = component<LayoutProps>("row");
export const Column = component<LayoutProps>("column");
export const Canvas = component<LayoutProps>("canvas");
export const Text = component<TextProps>("text");
export const Paragraph = component<ElementProps & { align?: "left" | "center" | "right" | "justify" }>("paragraph");
export const Run = component<ElementProps & { style?: TextStyle }>("run");
export const List = component<TextProps & { items: string[]; marker?: "bullet" | "number" }>("list");
export const Image = component<LayoutProps & { asset: AssetHandle; alt: string; fit?: "contain" | "cover" }>("image");
export const Shape = component<LayoutProps & { shape?: "rect" | "rounded_rect" | "ellipse"; fill?: string }>("shape");
export const Table = component<LayoutProps & { columns: Size[]; style?: TextStyle }>("table");
export const TableRow = component<ElementProps>("table-row");
export const Cell = component<ElementProps & { rowSpan?: number; colSpan?: number; fill?: string; style?: TextStyle }>("cell");
export const Chart = component<LayoutProps & {
  data: { categories: string[]; series: { key: string; name: string; values: number[] }[] };
  orientation?: "vertical" | "horizontal";
  legend?: "hidden" | "bottom" | "right";
  dataLabels?: "hidden" | "value";
}>("chart");
export const Connector = component<LayoutProps & {
  from: { target: { key: string }; anchor: "top" | "right" | "bottom" | "left" };
  to: { target: { key: string }; anchor: "top" | "right" | "bottom" | "left" };
  connectorType?: "straight" | "elbow";
}>("connector");
