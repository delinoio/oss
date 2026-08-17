import { defineConfig } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

export const ADMIN_PORT = 46306;
export const ADMIN_CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self' http://127.0.0.1:46307 https:",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
].join("; ");

export default defineConfig({
  plugins: [pluginReact()],
  source: { entry: { index: "./src/main.tsx" } },
  html: { template: "./index.html" },
  output: {
    assetPrefix: "/admin/",
    cleanDistPath: true,
    distPath: { root: "../../servers/devhud-api/internal/adminassets/dist" },
    filename: {
      css: "assets/[name].[contenthash:8].css",
      js: "assets/[name].[contenthash:8].js",
    },
    sourceMap: false,
  },
  performance: { chunkSplit: { strategy: "all-in-one" } },
  server: {
    host: "127.0.0.1",
    port: ADMIN_PORT,
    strictPort: true,
    headers: { "Content-Security-Policy": ADMIN_CSP },
  },
});
