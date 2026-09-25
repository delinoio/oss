import { extname } from "node:path";
import { Format } from "./types.js";

/** Compound extensions identify an atomic sprite bundle or a Figma receipt. */
export function matchesOutputExtension(format: Format, path: string): boolean {
  if (format === Format.Figma) return path.endsWith(".figma.json");
  if (format === Format.Sprite) return path.toLowerCase().endsWith(".sprite.zip");
  return extname(path).toLowerCase() === `.${format}`;
}
