import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { template: "./index.html" },
  server: { host: "localhost", port: 46308, strictPort: true },
  output: { sourceMap: false, filename: { js: "assets/[name].[contenthash:8].js", css: "assets/[name].[contenthash:8].css" } },
});
