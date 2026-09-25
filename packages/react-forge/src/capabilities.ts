import platforms from "./native-platforms.json" with { type: "json" };
import { Format, limits } from "./types.js";
/** Stable read-only capability data; no platform discovery, telemetry or I/O. */
export const capabilities = Object.freeze({
  runtime: Object.freeze({ node: "24", react: "19.2.8", reconciler: "0.33.0", hosts: Object.freeze(platforms.map(({ id, platform, architecture }) => Object.freeze({ id, platform, architecture }))) }),
  formats: Object.freeze({
    [Format.Glb]: Object.freeze({ generate: true, import: false, coordinateSpace: "world", staticOnly: true, embeddedTextures: true }),
    [Format.Fbx]: Object.freeze({ generate: true, import: false, coordinateSpace: "world", staticOnly: true, embeddedTextures: true, materialProfile: "blender-4.5" }),
    [Format.Pptx]: Object.freeze({ generate: true, import: true, coordinateSpace: "page" }),
    [Format.Docx]: Object.freeze({ generate: true, import: true, coordinateSpace: "word_flow" }),
    [Format.Xlsx]: Object.freeze({ generate: true, import: true, coordinateSpace: "worksheet" }),
    [Format.Figma]: Object.freeze({ generate: true, import: true, coordinateSpace: "page", remote: true }),
    [Format.Sprite]: Object.freeze({ generate: true, import: false, coordinateSpace: "sprite_frame", extension: ".sprite.zip", limits: Object.freeze({ dimension: 4096, scale: 16, padding: 64, frames: 1024, frameDurationMs: 60_000, sheetPixels: 64_000_000, totalFramePixels: 64_000_000, decodedImagePixels: 64_000_000, rasterPixels: 256_000_000 }) }),
    [Format.Wav]: Object.freeze({ generate: true, import: false, coordinateSpace: "timeline", sampleRates: Object.freeze([44100, 48000]), channels: Object.freeze([1, 2]), bitsPerSample: 16 }),
    [Format.Pdf]: Object.freeze({ generate: true, import: false, coordinateSpace: "page" }),
  }),
  limits,
  fonts: Object.freeze({ systemDiscovery: true, callerAssets: true, officeEmbedding: false, pdfSubsetEmbedding: true }),
  automaticTimeout: false,
  formulaCalculation: false,
  pdfUaConformance: false,
});
