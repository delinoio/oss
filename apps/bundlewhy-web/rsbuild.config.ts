import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

// The bundlewhy server and exported reports serve exactly one self-contained
// HTML file. Every script and style is inlined, nothing is split into separate
// chunks, and no resource hints point at other files.
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: {
    template: "./index.html",
    // Inline scripts cannot be deferred, so place them after the root element.
    inject: "body",
  },
  // Equivalent to the deprecated `performance.chunkSplit.strategy: "all-in-one"`.
  splitChunks: false,
  performance: {
    printFileSize: false,
  },
  output: {
    sourceMap: false,
    inlineScripts: true,
    inlineStyles: true,
    // Inline any referenced asset as a data URI instead of emitting a file.
    dataUriLimit: Number.MAX_SAFE_INTEGER,
    legalComments: "none",
  },
});
