import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { title: "FixKey" },
  server: { host: "127.0.0.1", port: 46321, strictPort: true, cors: false },
  output: { sourceMap: false },
});
