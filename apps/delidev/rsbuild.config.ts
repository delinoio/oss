import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

export default defineConfig({
  tools: { rspack: { module: { rules: [{ test: /ghostty-vt\.wasm$/, type: "asset/resource", generator: { filename: "static/wasm/[name].[contenthash][ext]" } }] } } },
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx", "tray-status": "./src/tray-status-main.tsx" } },
  html: { template: "./index.html" },
  server: { host: "127.0.0.1", port: 46311, strictPort: true },
  output: { assetPrefix: "./", sourceMap: false, cleanDistPath: true },
});
