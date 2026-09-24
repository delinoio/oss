import { Format, limits } from "./types.js";
/** Stable read-only capability data; no platform discovery, telemetry or I/O. */
export const capabilities = Object.freeze({
  runtime: Object.freeze({ node: "24", react: "19.2.8", reconciler: "0.33.0", platform: "darwin", architecture: "arm64" }),
  formats: Object.freeze({
    [Format.Pptx]: Object.freeze({ generate: true, import: true, coordinateSpace: "page" }),
    [Format.Docx]: Object.freeze({ generate: true, import: true, coordinateSpace: "word_flow" }),
    [Format.Xlsx]: Object.freeze({ generate: true, import: true, coordinateSpace: "worksheet" }),
    [Format.Pdf]: Object.freeze({ generate: true, import: false, coordinateSpace: "page" }),
  }),
  limits,
  fonts: Object.freeze({ systemDiscovery: true, callerAssets: true, officeEmbedding: false, pdfSubsetEmbedding: true }),
  automaticTimeout: false,
  formulaCalculation: false,
  pdfUaConformance: false,
});
