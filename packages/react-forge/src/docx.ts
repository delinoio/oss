import { createElement } from "react";
import type { AssetHandle, ElementProps, TextStyle } from "./types.js";

const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`docx:${name}`, props);
export const Document = component<ElementProps & { language?: string }>("document");
export const Section = component<ElementProps & { width?: number; height?: number; margin?: number }>("section");
export const Header = component<ElementProps>("header");
export const Footer = component<ElementProps>("footer");
export const Paragraph = component<ElementProps & { style?: TextStyle; heading?: number }>("paragraph");
export const Run = component<ElementProps & { style?: TextStyle }>("run");
export const Link = component<ElementProps & { href: string; style?: TextStyle }>("link");
export const List = component<ElementProps & { kind?: "bullet" | "number"; level?: number }>("list");
export const ListItem = component<ElementProps & { style?: TextStyle }>("list-item");
export const Table = component<ElementProps & { columns: number[] }>("table");
export const Row = component<ElementProps & { header?: boolean }>("row");
export const Cell = component<ElementProps & { rowSpan?: number; colSpan?: number; style?: TextStyle }>("cell");
export const Image = component<ElementProps & { asset: AssetHandle; width: number; height: number; alt: string }>("image");
export const Chart = component<ElementProps & {
  kind: "bar" | "line" | "pie";
  title?: string;
  categories: string[];
  series: { name: string; values: number[] }[];
  legend?: boolean;
  labels?: boolean;
  width: number;
  height: number;
  alt: string;
}>("chart");
export const PageBreak = component<ElementProps>("page-break");
