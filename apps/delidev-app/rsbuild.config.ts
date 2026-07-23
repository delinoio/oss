import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

export default defineConfig({
  plugins: [pluginReact()],
  html: {
    template: "./public/index.html",
  },
  output: {
    cleanDistPath: true,
    distPath: {
      root: "dist",
    },
  },
  source: {
    entry: {
      index: "./src/main.tsx",
    },
  },
  server: {
    historyApiFallback: true,
    port: 4173,
  },
});
