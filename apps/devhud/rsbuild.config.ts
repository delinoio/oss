import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

export const DEVHUD_DEVELOPMENT_CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src ws://127.0.0.1:46305",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-src 'none'",
  "worker-src 'none'",
].join("; ");

export default defineConfig({
  plugins: [pluginReact()],
  source: {
    entry: {
      index: "./src/main.tsx",
    },
  },
  html: {
    template: "./index.html",
  },
  output: {
    cleanDistPath: true,
    distPath: {
      root: "dist",
    },
    filename: {
      css: "assets/[name].[contenthash:8].css",
      js: "assets/[name].[contenthash:8].js",
    },
    sourceMap: false,
  },
  performance: {
    chunkSplit: {
      strategy: "all-in-one",
    },
  },
  server: {
    headers: {
      "Content-Security-Policy": DEVHUD_DEVELOPMENT_CSP,
    },
    host: "127.0.0.1",
    port: 46305,
    strictPort: true,
  },
});
