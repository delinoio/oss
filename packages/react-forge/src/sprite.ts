import { createElement } from "react";
import type { AssetHandle, ElementProps } from "./types.js";

const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`sprite:${name}`, props);
type Positioned = ElementProps & { x?: number; y?: number };
export interface Pivot { x: number; y: number }
export interface Crop { x: number; y: number; width: number; height: number }

/** A logical pixel canvas, enlarged with nearest-neighbor sampling on export. */
export const SpriteProject = component<ElementProps & {
  width: number; height: number; scale?: number; columns?: number; padding?: number;
  palette?: Readonly<Record<string, string>>;
}>("project");
export const Animation = component<ElementProps & { name: string; loop?: boolean }>("animation");
export const Frame = component<ElementProps & { durationMs: number; pivot?: Pivot }>("frame");
/** Translation and visibility apply to descendants in child drawing order. */
export const Layer = component<Positioned & { visible?: boolean }>("layer");
export const Pixel = component<Positioned & { fill: string }>("pixel");
export const Rect = component<Positioned & { width: number; height: number; fill: string }>("rect");
export const Ellipse = component<Positioned & { width: number; height: number; fill: string }>("ellipse");
/** Dot is transparent; other ASCII characters refer to the project's palette. */
export const PixelGrid = component<Positioned & { rows: readonly string[] }>("pixel-grid");
export const Image = component<Positioned & {
  asset: AssetHandle; width: number; height: number; source?: Crop; flipX?: boolean; flipY?: boolean;
}>("image");
