import { createElement } from "react";
import type { AssetHandle, ElementProps, TextStyle } from "./types.js";
const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`pdf:${name}`, props);
export const Document = component<ElementProps & { language: string; title?: string }>("document");
/** Flow continues onto automatically created pages with the same dimensions. */
export const Page = component<ElementProps & { width?: number; height?: number; margin?: number }>("page");
export const Paragraph = component<ElementProps & { style?: TextStyle; heading?: 1 | 2 | 3 | 4 | 5 | 6 }>("paragraph");
export const Text = Paragraph;
export const Run = component<ElementProps & { style?: TextStyle }>("run");
export const Link = component<ElementProps & { href: string; style?: TextStyle }>("link");
export const List = component<ElementProps & { ordered?: boolean }>("list");
export const ListItem = component<ElementProps & { style?: TextStyle }>("list-item");
export const Table = component<ElementProps & { columns: number[] }>("table");
/** Initial consecutive header rows repeat visually and appear once in reading order. */
export const Row = component<ElementProps & { header?: boolean }>("row");
export const Cell = component<ElementProps & { style?: TextStyle }>("cell");
export const Image = component<ElementProps & { asset: AssetHandle; width: number; height: number; alt: string }>("image");
/** Shapes without alternate text are decorative artifacts. */
export const Shape = component<ElementProps & { kind: "rectangle" | "ellipse" | "line"; width: number; height: number; fill?: string; stroke?: string; alt?: string }>("shape");
export const PageBreak = component<ElementProps>("page-break");
